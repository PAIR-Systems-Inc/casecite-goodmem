package app

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type connectorFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MIME     string `json:"mime_type"`
	Size     int64  `json:"size"`
	Modified string `json:"modified_at"`
	Path     string `json:"path"`
	Metadata M      `json:"metadata"`
}
type connectorPage struct {
	Files  []connectorFile
	Next   string
	Errors []M
}

var googleExportTypes = map[string][2]string{
	"application/vnd.google-apps.document":     {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	"application/vnd.google-apps.spreadsheet":  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
	"application/vnd.google-apps.presentation": {"application/vnd.openxmlformats-officedocument.presentationml.presentation", ".pptx"},
}

func connectorMIME(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	// The single-binary image need not ship an OS MIME database.
	if value := map[string]string{
		".pdf": "application/pdf", ".txt": "text/plain", ".md": "text/markdown",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".odt":  "application/vnd.oasis.opendocument.text", ".doc": "application/msword",
		".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
		".msg": "application/vnd.ms-outlook", ".eml": "message/rfc822", ".rtf": "application/rtf",
		".csv": "text/csv", ".tsv": "text/tab-separated-values", ".xml": "application/xml",
		".html": "text/html", ".htm": "text/html", ".json": "application/json",
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".tif": "image/tiff", ".tiff": "image/tiff",
	}[ext]; value != "" {
		return value
	}
	value := mime.TypeByExtension(ext)
	if value != "" {
		value, _, _ = mime.ParseMediaType(value)
	}
	return fallback(value, "application/octet-stream")
}
func connectorModified(raw any) string {
	// A missing timestamp is unknown, not "now": the latter silently creates
	// a new apparent remote revision on every listing.
	if value, ok := raw.(string); ok {
		if stamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return stamp.UTC().Format(time.RFC3339Nano)
		}
	}
	return ""
}
func connectorMSID(value string) (string, string, error) {
	if !strings.HasPrefix(value, "drv:") {
		return "", value, nil
	}
	parts := strings.SplitN(value, ":", 3)
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return "", "", bad("Invalid Microsoft drive item identifier")
	}
	return parts[1], parts[2], nil
}
func connectorParseFile(provider string, item M, scope string) connectorFile {
	f := connectorFile{ID: chatSourceID(item["id"]), Name: str(item, "name"), MIME: str(item, "mimeType"), Size: int64(judgeNumber(item, "size")), Metadata: M{}}
	switch provider {
	case "google_drive":
		f.Modified = connectorModified(item["modifiedTime"])
		parents := stringsOf(item["parents"])
		if parents == nil {
			parents = []string{}
		}
		f.Metadata = M{"parents": parents, "google_mime_type": f.MIME}
		if f.MIME == "application/vnd.google-apps.folder" {
			f.MIME = "folder"
		}
		if export, ok := googleExportTypes[f.MIME]; ok {
			f.MIME = export[0]
			if !strings.HasSuffix(strings.ToLower(f.Name), export[1]) {
				f.Name += export[1]
			}
		}
	case "onedrive":
		if scope != "" {
			f.ID = "drv:" + scope + ":" + f.ID
		}
		if _, folder := item["folder"]; folder {
			f.MIME = "folder"
		} else {
			f.MIME = fallback(str(object(item["file"]), "mimeType"), "application/octet-stream")
		}
		f.Modified = connectorModified(item["lastModifiedDateTime"])
		f.Path = str(object(item["parentReference"]), "path")
		f.Metadata = M{"webUrl": item["webUrl"]}
	case "box":
		f.MIME = connectorMIME(f.Name)
		if item["type"] == "folder" {
			f.MIME = "folder"
		}
		f.Modified = connectorModified(item["modified_at"])
		f.Metadata = M{"type": item["type"]}
	case "dropbox":
		f.ID = fallback(str(item, "path_lower"), f.ID)
		f.MIME = connectorMIME(f.Name)
		if item[".tag"] == "folder" {
			f.MIME = "folder"
			f.Size = 0
		}
		f.Modified = connectorModified(fallback(str(item, "server_modified"), str(item, "client_modified")))
		f.Path = str(item, "path_display")
		f.Metadata = M{"tag": item[".tag"]}
	case "clio":
		if filepath.Ext(f.Name) == "" && str(item, "filename") != "" {
			f.Name = str(item, "filename")
		}
		f.MIME = fallback(str(item, "content_type"), connectorMIME(f.Name))
		f.Modified = connectorModified(item["updated_at"])
		matter := object(item["matter"])
		f.Metadata = M{"clio_matter_id": matter["id"], "clio_matter": matter["display_number"]}
	case "filevine":
		if scope == "" {
			f.ID = connectorNative(item["projectId"])
			f.Name = fallback(str(item, "projectName"), "Project "+f.ID)
			f.MIME = "folder"
			f.Metadata = M{"kind": "project"}
		} else {
			f.ID = connectorNative(item["docId"])
			f.Name = str(item, "filename")
			ext := strings.TrimPrefix(str(item, "extension"), ".")
			if ext != "" && !strings.HasSuffix(strings.ToLower(f.Name), "."+strings.ToLower(ext)) {
				f.Name += "." + ext
			}
			f.MIME = connectorMIME(f.Name)
			f.Modified = connectorModified(item["lastUpdated"])
			f.Metadata = M{"project_id": scope, "project_name": "", "doc_type": item["docType"], "uploaded_by": item["uploadedBy"]}
		}
	case "netdocuments":
		if scope == "" {
			f.ID = "cabinet:" + f.ID
			f.MIME = "folder"
			f.Metadata = M{"kind": "cabinet"}
		} else if strings.HasPrefix(scope, "cabinet:") {
			f.MIME = "folder"
			f.Metadata = M{"cabinet": strings.TrimPrefix(scope, "cabinet:")}
		} else {
			f.MIME = fallback(f.MIME, connectorMIME(f.Name))
			if item["type"] == "folder" {
				f.MIME = "folder"
			}
			f.Modified = connectorModified(object(item["modified"])["date"])
			f.Metadata = M{"cabinet": item["cabinet"], "version": item["version"], "extension": item["extension"]}
		}
	case "imanage":
		ext := strings.TrimPrefix(strings.ToLower(str(item, "extension")), ".")
		if ext != "" && !strings.HasSuffix(strings.ToLower(f.Name), "."+ext) {
			f.Name += "." + ext
		}
		f.MIME = connectorMIME(f.Name)
		f.Modified = connectorModified(item["edit_date"])
		f.Metadata = M{"library": item["database"], "version": item["version"], "class": item["class"], "author": item["author"]}
	}
	if f.MIME == "folder" {
		f.Modified = ""
	}
	return f
}
func connectorOffset(cursor string, fallbackValue int) (int, error) {
	if cursor == "" {
		return fallbackValue, nil
	}
	value, err := strconv.Atoi(cursor)
	if err != nil || value < 0 || value > 1_000_000_000 {
		return 0, bad("Invalid connector page token")
	}
	return value, nil
}

func (s *Server) connectorList(ctx context.Context, user string, d connectorDefinition, v map[string]string, generation, folder, cursor string) (connectorPage, error) {
	out := connectorPage{Files: []connectorFile{}, Errors: []M{}}
	account, err := s.connectorCredentials(ctx, user, d, v, generation)
	if err != nil {
		return out, err
	}
	creds := account.Credentials
	o := connectorOAuthEndpoints(d, v)
	h := connectorHeaders(d, creds)
	method, target, key, scope := "GET", "", "", folder
	params := url.Values{}
	var payload []byte
	offset := 0
	switch d.ID {
	case "google_drive":
		query := "trashed = false"
		if folder != "" {
			query += " and '" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(folder) + "' in parents"
		}
		params = url.Values{"q": {query}, "fields": {"nextPageToken,incompleteSearch,files(id,name,mimeType,size,modifiedTime,parents)"}, "pageSize": {"100"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"}}
		if cursor != "" {
			params.Set("pageToken", cursor)
		}
		target = o.API + "/drive/v3/files"
		key = "files"
	case "onedrive":
		scope = ""
		key = "value"
		if cursor != "" {
			var next string
			var found bool
			scope, next, found = strings.Cut(cursor, "|")
			if !found {
				return out, bad("Invalid Microsoft page token")
			}
			target, err = connectorPageURL(next, o.API)
			if err != nil {
				return out, err
			}
		} else {
			switch {
			case folder == "":
				target = o.API + "/me/drive/root/children"
			case strings.HasPrefix(folder, "drive:"):
				scope = strings.TrimPrefix(folder, "drive:")
				target = o.API + "/drives/" + url.PathEscape(scope) + "/root/children"
			default:
				var item string
				scope, item, err = connectorMSID(folder)
				if err != nil {
					return out, err
				}
				if scope == "" {
					target = o.API + "/me/drive/items/" + url.PathEscape(item) + "/children"
				} else {
					target = o.API + "/drives/" + url.PathEscape(scope) + "/items/" + url.PathEscape(item) + "/children"
				}
			}
			params.Set("$top", "100")
		}
	case "box":
		target = o.API + "/folders/" + url.PathEscape(fallback(folder, "0")) + "/items"
		key = "entries"
		params = url.Values{"fields": {"id,name,type,size,modified_at"}, "limit": {"100"}, "usemarker": {"true"}}
		if cursor != "" {
			params.Set("marker", cursor)
		}
	case "dropbox":
		method = "POST"
		key = "entries"
		h.Set("Content-Type", "application/json")
		if cursor != "" {
			target = o.API + "/files/list_folder/continue"
			payload = jsonBytes(M{"cursor": cursor})
		} else {
			target = o.API + "/files/list_folder"
			payload = jsonBytes(M{"path": folder, "recursive": false, "limit": 100})
		}
	case "clio":
		if folder != "" {
			return out, bad("Clio cannot sync a single folder")
		}
		key = "data"
		if cursor != "" {
			target, err = connectorPageURL(cursor, o.API)
			if err != nil {
				return out, err
			}
		} else {
			target = o.API + "/documents.json"
			params = url.Values{"fields": {"id,name,filename,content_type,size,updated_at,matter{id,display_number}"}, "limit": {"100"}, "order": {"id(asc)"}}
		}
	case "filevine":
		offset, err = connectorOffset(cursor, 1)
		if err != nil {
			return out, err
		}
		target = o.API + "/core/projects"
		if folder != "" {
			target += "/" + url.PathEscape(folder) + "/docs"
		}
		key = "items"
		params = url.Values{"requestedPage": {strconv.Itoa(offset)}, "pageSize": {"50"}}
	case "netdocuments":
		offset, err = connectorOffset(cursor, 0)
		if err != nil {
			return out, err
		}
		key = "list"
		switch {
		case folder == "":
			target = o.API + "/Cabinet"
			key = "cabinets"
		case strings.HasPrefix(folder, "cabinet:"):
			target = o.API + "/Cabinet/" + url.PathEscape(strings.TrimPrefix(folder, "cabinet:")) + "/folders"
		default:
			target = o.API + "/Folder/" + url.PathEscape(folder) + "/contents"
		}
		params = url.Values{"$top": {"100"}, "$skip": {strconv.Itoa(offset)}}
	case "imanage":
		offset, err = connectorOffset(cursor, 0)
		if err != nil {
			return out, err
		}
		target = o.API + "/customers/" + url.PathEscape(chatSourceID(creds["customer_id"]))
		if folder != "" {
			target += "/folders/" + url.PathEscape(folder)
		}
		target += "/documents"
		key = "data"
		params = url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if folder == "" {
			params.Set("sort", "edit_date:desc")
		}
	}
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	data, err := s.connectorJSON(ctx, method, target, h, payload, true)
	if err != nil {
		return out, err
	}
	rows, ok := data[key].([]any)
	if !ok {
		return out, fail(502, "The connector response omitted its file listing")
	}
	for _, raw := range rows {
		item := object(raw)
		f := connectorParseFile(d.ID, item, scope)
		if f.ID == "" || f.ID == "cabinet:" || f.Name == "" {
			out.Errors = append(out.Errors, M{"name": fallback(f.Name, "Unnamed file"), "reason": "Provider returned an incomplete file entry"})
			continue
		}
		// The account-wide Drive list is already flat; recursing its folders
		// repeats the same files and misses no additional scope.
		if d.ID == "google_drive" && folder == "" && f.MIME == "folder" {
			continue
		}
		out.Files = append(out.Files, f)
	}
	switch d.ID {
	case "google_drive":
		out.Next = str(data, "nextPageToken")
		if data["incompleteSearch"] == true {
			out.Errors = append(out.Errors, M{"name": "Google Drive", "reason": "The provider reported an incomplete search; some shared-drive files may be missing"})
		}
	case "onedrive":
		if next := str(data, "@odata.nextLink"); next != "" {
			if _, err = connectorPageURL(next, o.API); err != nil {
				return out, err
			}
			out.Next = scope + "|" + next
		}
		if folder == "" && cursor == "" {
			drives, errors := s.connectorSharePointDrives(ctx, o.API, h)
			out.Files = append(out.Files, drives...)
			out.Errors = append(out.Errors, errors...)
		}
	case "box":
		out.Next = str(data, "next_marker")
	case "dropbox":
		if data["has_more"] == true {
			out.Next = str(data, "cursor")
			if out.Next == "" {
				return out, fail(502, "Dropbox omitted its continuation cursor")
			}
		}
	case "clio":
		out.Next = str(object(object(data["meta"])["paging"]), "next")
		if out.Next != "" {
			if _, err = connectorPageURL(out.Next, o.API); err != nil {
				return out, err
			}
		}
	case "filevine":
		if data["hasMore"] == true {
			out.Next = strconv.Itoa(offset + 1)
		}
	case "netdocuments", "imanage":
		totalKey := "total"
		if d.ID == "imanage" {
			totalKey = "total_count"
		}
		if offset+len(rows) < number(data, totalKey, 0) {
			if len(rows) == 0 {
				return out, fail(502, "The provider returned an empty page before its reported end")
			}
			out.Next = strconv.Itoa(offset + len(rows))
		}
	}
	return out, nil
}

func (s *Server) connectorSharePointDrives(ctx context.Context, base string, h http.Header) ([]connectorFile, []M) {
	out := []connectorFile{}
	failures := []M{}
	seen := map[string]bool{}
	seenDrive := map[string]bool{}
	next := base + "/sites?" + url.Values{"search": {"*"}, "$select": {"id,displayName"}}.Encode()
	for next != "" {
		if seen[next] {
			failures = append(failures, M{"name": "SharePoint", "reason": "Repeated sites page stopped"})
			break
		}
		seen[next] = true
		if _, err := connectorPageURL(next, base); err != nil {
			failures = append(failures, M{"name": "SharePoint", "reason": connectorSafeError(err)})
			break
		}
		sites, err := s.connectorJSON(ctx, "GET", next, h, nil, true)
		if err != nil {
			failures = append(failures, M{"name": "SharePoint", "reason": "Site discovery unavailable; OneDrive files can still be imported"})
			break
		}
		for _, site := range maps(sites["value"]) {
			id := str(site, "id")
			if id == "" {
				continue
			}
			driveURL := base + "/sites/" + url.PathEscape(id) + "/drives?%24select=id%2Cname"
			for driveURL != "" {
				if seen[driveURL] {
					failures = append(failures, M{"name": site["displayName"], "reason": "Repeated drive page stopped"})
					break
				}
				seen[driveURL] = true
				if _, err = connectorPageURL(driveURL, base); err != nil {
					failures = append(failures, M{"name": site["displayName"], "reason": connectorSafeError(err)})
					break
				}
				drives, err := s.connectorJSON(ctx, "GET", driveURL, h, nil, true)
				if err != nil {
					failures = append(failures, M{"name": site["displayName"], "reason": connectorSafeError(err)})
					break
				}
				for _, drive := range maps(drives["value"]) {
					id := str(drive, "id")
					if id == "" || seenDrive[id] {
						continue
					}
					seenDrive[id] = true
					out = append(out, connectorFile{ID: "drive:" + id, Name: fallback(str(site, "displayName"), "SharePoint") + " — " + fallback(str(drive, "name"), "Documents"), MIME: "folder", Metadata: M{"kind": "sharepoint_drive", "site": site["displayName"]}})
				}
				driveURL = str(drives, "@odata.nextLink")
			}
		}
		next = str(sites, "@odata.nextLink")
	}
	return out, failures
}

func connectorASCIIJSON(value any) string {
	// Dropbox-API-Arg must be ASCII even when the filename contains emoji.
	raw := string(jsonBytes(value))
	var out strings.Builder
	for _, r := range raw {
		if r < 128 {
			out.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			r -= 0x10000
			fmt.Fprintf(&out, "\\u%04x\\u%04x", 0xd800+(r>>10), 0xdc00+(r&1023))
		}
	}
	return out.String()
}
func (s *Server) connectorFileBytes(ctx context.Context, user string, d connectorDefinition, v map[string]string, generation string, file connectorFile) ([]byte, error) {
	account, err := s.connectorCredentials(ctx, user, d, v, generation)
	if err != nil {
		return nil, err
	}
	o := connectorOAuthEndpoints(d, v)
	creds := account.Credentials
	h := connectorHeaders(d, creds)
	target, method := "", "GET"
	var hosts []string
	switch d.ID {
	case "google_drive":
		base := o.API + "/drive/v3/files/" + url.PathEscape(file.ID)
		data, err := s.connectorJSON(ctx, "GET", base+"?fields=mimeType&supportsAllDrives=true", h, nil, true)
		if err != nil {
			return nil, err
		}
		if export, ok := googleExportTypes[str(data, "mimeType")]; ok {
			target = base + "/export?" + url.Values{"mimeType": {export[0]}}.Encode()
		} else {
			target = base + "?alt=media&supportsAllDrives=true"
		}
		hosts = []string{"googleapis.com", "googleusercontent.com"}
	case "onedrive":
		drive, item, err := connectorMSID(file.ID)
		if err != nil {
			return nil, err
		}
		base := o.API + "/me/drive/items/" + url.PathEscape(item)
		if drive != "" {
			base = o.API + "/drives/" + url.PathEscape(drive) + "/items/" + url.PathEscape(item)
		}
		target = base + "/content"
		hosts = []string{"graph.microsoft.com", "sharepoint.com", "1drv.com", "svc.ms", "live.com"}
	case "box":
		target = o.API + "/files/" + url.PathEscape(file.ID) + "/content"
		hosts = []string{"box.com", "boxcloud.com"}
	case "dropbox":
		target = "https://content.dropboxapi.com/2/files/download"
		method = "POST"
		h.Set("Dropbox-API-Arg", connectorASCIIJSON(M{"path": file.ID}))
		hosts = []string{"dropboxapi.com", "dropboxusercontent.com", "dropbox.com"}
	case "clio":
		target = o.API + "/documents/" + url.PathEscape(file.ID) + "/download.json"
	case "filevine":
		target = o.API + "/core/docs/" + url.PathEscape(file.ID) + "/download"
	case "netdocuments":
		target = o.API + "/Document/" + url.PathEscape(file.ID) + "/content"
	case "imanage":
		target = o.API + "/customers/" + url.PathEscape(chatSourceID(creds["customer_id"])) + "/documents/" + url.PathEscape(file.ID) + "/download"
	}
	return s.connectorDownload(ctx, method, target, h, hosts, o.API)
}
