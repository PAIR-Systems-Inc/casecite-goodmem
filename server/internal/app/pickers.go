package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

func (s *Server) pickerRoutes() {
	s.api("GET /api/v1/pickers/config", false, s.pickerConfig)
	s.api("GET /api/v1/pickers/box/token", false, s.boxPickerToken)
	for _, provider := range []string{"google", "microsoft", "box", "dropbox"} {
		s.api("POST /api/v1/pickers/"+provider+"/import", false, func(q request) (any, error) { return s.pickerImport(q, provider) })
	}
}
func (s *Server) pickerConfig(q request) (any, error) {
	values := map[string]string{}
	for _, provider := range []string{"google", "microsoft", "box", "dropbox"} {
		v, err := s.connectorConfig(q.r.Context(), provider)
		if err != nil {
			return nil, err
		}
		for _, field := range connectorAdminFields[provider] {
			values[field] = v[field]
		}
	}
	missing := []string{}
	for _, field := range []string{"google_client_id", "google_api_key"} {
		if values[field] == "" {
			missing = append(missing, field)
		}
	}
	out := M{"google_enabled": len(missing) == 0, "google_configured": len(missing) == 0, "google_missing": missing, "microsoft_enabled": values["microsoft_client_id"] != "", "microsoft_tenant_id": fallback(values["microsoft_tenant_id"], "common"), "box_enabled": values["box_client_id"] != "", "dropbox_enabled": values["dropbox_app_key"] != ""}
	for _, field := range []string{"google_api_key", "google_client_id", "google_app_id", "microsoft_client_id", "box_client_id", "dropbox_app_key"} {
		out[field] = nullable(values[field])
	}
	return out, nil
}
func (s *Server) boxPickerToken(q request) (any, error) {
	d, _ := s.connectorDefinition("box")
	v, err := s.connectorConfig(q.r.Context(), "box")
	if err != nil {
		return nil, err
	}
	if !s.connectorConfigured(d, v) {
		return nil, bad("Box is not configured on this server")
	}
	account, err := s.connectorCredentials(q.r.Context(), q.user.ID, d, v, "")
	if err != nil {
		return nil, fail(409, "Connect Box before opening its file picker")
	}
	payload := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token": {str(account.Credentials, "access_token")}, "subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "scope": {"base_picker item_download root_readonly"}}
	data, err := s.connectorJSON(q.r.Context(), "POST", connectorOAuthEndpoints(d, v).Token, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(payload.Encode()), false)
	if err != nil || str(data, "access_token") == "" {
		return nil, fail(502, "Could not obtain a Box picker token")
	}
	q.w.Header().Set("Cache-Control", "no-store")
	return M{"access_token": data["access_token"], "expires_in": number(data, "expires_in", 3600)}, nil
}
func validatePickerFiles(provider string, raw any) ([]M, error) {
	rows, ok := raw.([]any)
	if !ok {
		return nil, fail(422, "files must be an array")
	}
	required := map[string][]string{"google": {"id", "name", "mimeType", "oauthToken"}, "microsoft": {"id", "name", "accessToken"}, "box": {"id", "name", "accessToken"}, "dropbox": {"name", "link"}}[provider]
	optional := map[string][]string{"google": {"url", "sizeBytes"}, "microsoft": {"size", "webUrl", "downloadUrl", "driveId", "sharepointEndpoint"}, "box": {"size"}, "dropbox": {"bytes", "icon"}}[provider]
	out := []M{}
	for _, row := range rows {
		in, ok := row.(map[string]any)
		if !ok {
			return nil, fail(422, "Each selected file must be an object")
		}
		file := M{}
		for _, key := range required {
			value, ok := in[key].(string)
			if !ok {
				return nil, fail(422, "Selected file is missing "+key)
			}
			file[key] = value
		}
		for _, key := range optional {
			file[key] = nil
			if raw := in[key]; raw != nil {
				if key == "size" || key == "sizeBytes" || key == "bytes" {
					value, err := chatInt(raw, -1<<52, 1<<52)
					if err != nil {
						return nil, fail(422, "Invalid file size")
					}
					file[key] = value
				} else {
					value, ok := raw.(string)
					if !ok {
						return nil, fail(422, "Invalid selected file "+key)
					}
					file[key] = value
				}
			}
		}
		out = append(out, file)
	}
	return out, nil
}
func (s *Server) pickerImport(q request, provider string) (any, error) {
	var raw any
	if err := json.NewDecoder(q.r.Body).Decode(&raw); err != nil {
		return nil, bad("Invalid picker request")
	}
	files, err := validatePickerFiles(provider, raw)
	if err != nil {
		return nil, err
	}
	if q.r.URL.Query().Get("async") == "true" {
		encrypted, err := s.crypt(string(jsonBytes(files)), false)
		if err != nil {
			return nil, err
		}
		id, err := s.enqueueTask(q.r.Context(), q.user.ID, "picker.import", M{"provider": provider, "encrypted_files": encrypted})
		if err != nil {
			return nil, err
		}
		writeJSON(q.w, 202, M{"job_id": id, "status": "pending"})
		return nil, nil
	}
	return s.executePickerFiles(q.r.Context(), q.user.ID, provider, files)
}
func (s *Server) executePickerImport(ctx context.Context, user string, input M) (M, error) {
	plain, err := s.crypt(str(input, "encrypted_files"), true)
	if err != nil {
		return nil, err
	}
	var files []M
	if err = json.Unmarshal([]byte(plain), &files); err != nil {
		return nil, err
	}
	result, err := s.executePickerFiles(ctx, user, str(input, "provider"), files)
	if err == nil {
		if outputs, ok := ctx.Value(jobOutputsKey{}).(*jobOutputs); ok {
			outputs.Lock()
			outputs.Picker = true
			outputs.Unlock()
		}
	}
	return result, err
}
func (s *Server) pickerAllowed(ctx context.Context, user string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u, err := s.loadUser(ctx, user)
	if err != nil {
		return fail(403, "The account is no longer available")
	}
	if !slices.Contains(u.Permissions, "documents.upload") || u.MustChangePassword || u.MFAEnrollmentRequired {
		return fail(403, "Document upload is no longer permitted for this account")
	}
	if lease, ok := ctx.Value(jobLeaseKey{}).(jobLease); ok {
		var allowed bool
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_jobs WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>now())`, lease.ID, lease.Token).Scan(&allowed)
		if err != nil {
			return err
		}
		if !allowed {
			return context.Canceled
		}
	}
	return nil
}
func pickerDownload(provider string, file M) (target, source, id, name string, headers http.Header, hosts []string) {
	id, name = str(file, "id"), str(file, "name")
	headers = http.Header{}
	switch provider {
	case "google":
		source = "google_picker"
		headers = connectorBearer(str(file, "oauthToken"))
		target = "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(id)
		if export, ok := googleExportTypes[str(file, "mimeType")]; ok {
			target += "/export?" + url.Values{"mimeType": {export[0]}}.Encode()
			if !strings.HasSuffix(strings.ToLower(name), export[1]) {
				name += export[1]
			}
		} else {
			target += "?alt=media&supportsAllDrives=true"
		}
		hosts = []string{"googleapis.com", "googleusercontent.com"}
	case "microsoft":
		source = "onedrive_picker"
		hosts = []string{"graph.microsoft.com", "sharepoint.com", "1drv.com", "svc.ms", "live.com"}
		if drive := str(file, "driveId"); drive != "" {
			id = "drv:" + drive + ":" + id
			target = "https://graph.microsoft.com/v1.0/drives/" + url.PathEscape(drive) + "/items/" + url.PathEscape(str(file, "id")) + "/content"
		} else {
			target = "https://graph.microsoft.com/v1.0/me/drive/items/" + url.PathEscape(id) + "/content"
		}
		if direct := str(file, "downloadUrl"); direct != "" {
			target = direct
		} else {
			headers = connectorBearer(str(file, "accessToken"))
		}
	case "box":
		source = "box_picker"
		target = "https://api.box.com/2.0/files/" + url.PathEscape(id) + "/content"
		headers = connectorBearer(str(file, "accessToken"))
		hosts = []string{"box.com", "boxcloud.com"}
	case "dropbox":
		source = "dropbox_chooser"
		target = str(file, "link")
		id = "link:" + digest(target)
		hosts = []string{"dropbox.com", "dropboxusercontent.com"}
	}
	return
}
func (s *Server) importPickerFile(ctx context.Context, user, provider string, file M) (M, error) {
	if err := s.pickerAllowed(ctx, user); err != nil {
		return nil, err
	}
	for _, field := range []string{"size", "sizeBytes", "bytes"} {
		if judgeNumber(file, field) > float64(s.cfg.MaxUpload) {
			return nil, bad("File exceeds the upload limit")
		}
	}
	target, source, sourceID, name, headers, hosts := pickerDownload(provider, file)
	if target == "" {
		return nil, bad("Unknown picker provider")
	}
	data, err := s.connectorDownload(ctx, "GET", target, headers, hosts, "")
	if err != nil {
		return nil, err
	}
	if err = s.pickerAllowed(ctx, user); err != nil {
		return nil, err
	}
	id := connectorDocumentID(user, source, sourceID, data)
	select {
	case s.uploads <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	metadata := M{}
	// Store only the provider's ordinary web link, never a signed download URL.
	if provider == "google" && str(file, "url") != "" {
		metadata["original_url"] = str(file, "url")
	}
	if provider == "microsoft" && str(file, "webUrl") != "" {
		metadata["web_url"] = str(file, "webUrl")
	}
	doc, created, err := s.beginDocumentIngest(ctx, user, "", id, name, source, sourceID, data, metadata)
	<-s.uploads
	if guard := s.pickerAllowed(ctx, user); guard != nil {
		if created {
			s.discardConnectorImport(user, id)
		}
		return nil, guard
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if created && ctx.Err() != nil {
			s.discardConnectorImport(user, id)
		}
	}()
	if str(doc, "status") != "indexed" {
		if doc, err = s.waitDocumentIndexed(ctx, user, id); err != nil {
			return nil, err
		}
	}
	if err = s.pickerAllowed(ctx, user); err != nil {
		if created {
			s.discardConnectorImport(user, id)
		}
		return nil, err
	}
	return doc, nil
}
func (s *Server) executePickerFiles(ctx context.Context, user, provider string, files []M) (M, error) {
	if !slices.Contains([]string{"google", "microsoft", "box", "dropbox"}, provider) {
		return nil, bad("Unknown picker provider")
	}
	imported, failed := 0, 0
	errors := []M{}
	ids := []string{}
	for i, file := range files {
		if err := s.pickerAllowed(ctx, user); err != nil {
			return nil, err
		}
		if lease, ok := ctx.Value(jobLeaseKey{}).(jobLease); ok {
			s.jobProgress(ctx, lease.ID, "Importing "+str(file, "name"), float64(i)/float64(max(1, len(files))))
		}
		doc, err := s.importPickerFile(ctx, user, provider, file)
		if err != nil {
			if auth := s.pickerAllowed(ctx, user); auth != nil {
				return nil, auth
			}
			failed++
			if len(errors) < 20 {
				errors = append(errors, M{"name": str(file, "name"), "reason": connectorSafeError(err)})
			}
			continue
		}
		imported++
		ids = append(ids, str(doc, "id"))
	}
	out := M{"imported": imported, "failed": failed, "errors": errors, "document_ids": ids, "access_document_ids": ids}
	s.audit(ctx, user, "document.upload", "picker_import", provider, M{"imported": imported, "failed": failed, "document_ids": ids})
	return out, nil
}
