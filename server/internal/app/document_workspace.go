package app

import (
	"context"
	"path"
	"slices"
	"strings"
)

func folderPath(raw string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if value == "" || value == "/" {
		return "", nil
	}
	if len(value) > 1000 {
		return "", bad("Folder path is too long")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." {
			return "", bad("Folder paths cannot contain . or ..")
		}
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return "", bad("Folder names cannot contain control characters")
		}
	}
	return "/" + strings.Trim(path.Clean("/"+value), "/") + "/", nil
}

func (s *Server) folders(q request) (any, error) {
	result := M{}
	if q.r.Method == "POST" {
		m, err := body(q)
		if err != nil {
			return nil, err
		}
		folder, err := folderPath(str(m, "name"))
		if err != nil {
			return nil, err
		}
		if folder == "" {
			return nil, bad("A folder name is required")
		}
		tx, err := s.db.BeginTx(q.r.Context(), nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		parts := strings.Split(strings.Trim(folder, "/"), "/")
		for i := range parts {
			p := "/" + strings.Join(parts[:i+1], "/") + "/"
			if _, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_folders(owner_id,path) VALUES($1,$2) ON CONFLICT DO NOTHING`, q.user.ID, p); err != nil {
				return nil, err
			}
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		result["path"] = folder
	} else if q.r.Method == "DELETE" {
		folder, err := folderPath(q.r.URL.Query().Get("path"))
		if err != nil {
			return nil, err
		}
		if folder == "" {
			return nil, bad("Choose a folder to remove")
		}
		tx, err := s.db.BeginTx(q.r.Context(), nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_objects SET data=jsonb_set(data,'{folder_path}','null'),updated_at=now() WHERE kind='document' AND owner_id=$1 AND starts_with(data->>'folder_path',$2)`, q.user.ID, folder); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_folders WHERE owner_id=$1 AND starts_with(path,$2)`, q.user.ID, folder); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		result["status"] = "deleted"
		s.audit(q.r.Context(), q.user.ID, "folder.delete", "folder", folder, M{})
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT path FROM cc_folders WHERE owner_id=$1 ORDER BY path`, q.user.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	folders := []string{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return nil, err
		}
		folders = append(folders, p)
	}
	result["folders"] = folders
	return result, rows.Err()
}

func (s *Server) moveDocumentFolder(q request) (any, error) {
	doc, err := s.document(q)
	if err != nil {
		return nil, err
	}
	if err = own(q, doc); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	folder, err := folderPath(str(m, "folder_path"))
	if err != nil {
		return nil, err
	}
	if folder != "" {
		var exists bool
		if err = s.db.QueryRowContext(q.r.Context(), `SELECT EXISTS(SELECT 1 FROM cc_folders WHERE owner_id=$1 AND path=$2)`, q.user.ID, folder).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, missing()
		}
	}
	_, err = s.db.ExecContext(q.r.Context(), `UPDATE cc_objects SET data=jsonb_set(data,'{folder_path}',$1),updated_at=now() WHERE kind='document' AND id=$2 AND owner_id=$3`, jsonBytes(nullable(folder)), str(doc, "id"), q.user.ID)
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "document.move", "document", str(doc, "id"), M{})
	return M{"status": "moved", "folder_path": nullable(folder)}, nil
}

func (s *Server) documentTree(q request) (any, error) {
	docs, err := s.objects(q.r.Context(), q.user.ID, "document")
	if err != nil {
		return nil, err
	}
	roots := []M{}
	folders := map[string]M{}
	sources := []string{}
	var ensureFolder func(string) M
	ensureFolder = func(p string) M {
		if existing, ok := folders[p]; ok {
			return existing
		}
		name := path.Base(strings.TrimSuffix(p, "/"))
		node := M{"id": p, "name": name, "type": "folder", "path": p, "children": []M{}, "document_count": 0}
		folders[p] = node
		parent := path.Dir(strings.TrimSuffix(p, "/"))
		if parent == "/" {
			roots = append(roots, node)
		} else {
			up := ensureFolder(parent + "/")
			up["children"] = append(up["children"].([]M), node)
		}
		return node
	}
	stored, err := s.folders(q)
	if err != nil {
		return nil, err
	}
	for _, p := range stringsOf(object(stored)["folders"]) {
		ensureFolder(p)
	}
	for _, doc := range docs {
		folder := str(doc, "folder_path")
		filename := str(doc, "filename")
		source := str(doc, "source")
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
		file := M{"id": doc["id"], "name": filename, "type": "file", "path": folder + filename, "source": source, "doc_type": strings.TrimPrefix(path.Ext(filename), "."), "size": doc["size"], "children": []M{}, "document_count": 0}
		if folder == "" {
			file["path"] = "/" + filename
			roots = append(roots, file)
		} else {
			node := ensureFolder(folder)
			node["children"] = append(node["children"].([]M), file)
			for p := folder; p != "/" && p != "."; {
				n := ensureFolder(p)
				n["document_count"] = number(n, "document_count", 0) + 1
				parent := path.Dir(strings.TrimSuffix(p, "/"))
				if parent == "/" {
					break
				}
				p = parent + "/"
			}
		}
	}
	slices.Sort(sources)
	return M{"tree": roots, "total_documents": len(docs), "total_folders": len(folders), "sources": sources}, nil
}

func (s *Server) canManageDocument(ctx context.Context, user string, doc M) error {
	if str(doc, "user_id") == user {
		return nil
	}
	if str(doc, "matter_id") == "" {
		return missing()
	}
	return s.checkMatter(ctx, user, str(doc, "matter_id"), true)
}

func (s *Server) documentWorkspaceRoutes() {
	s.api("GET /api/v1/documents/tree", false, s.documentTree)
	s.api("GET /api/v1/documents/folders", false, s.folders)
	s.api("POST /api/v1/documents/folders", false, s.folders)
	s.api("DELETE /api/v1/documents/folders", false, s.folders)
	s.api("POST /api/v1/documents/{id}/move", false, s.moveDocumentFolder)
}
