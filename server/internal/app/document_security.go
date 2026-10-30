package app

import (
	"context"
	"crypto/sha256"
	"fmt"
)

func spaceEmbedderInput(space M) []M {
	out := []M{}
	for _, e := range maps(space["spaceEmbedders"]) {
		out = append(out, M{"embedderId": e["embedderId"], "defaultRetrievalWeight": e["defaultRetrievalWeight"]})
	}
	return out
}

// A create-once GoodMem resource binds a reserve credential to one application
// user even if someone mistakenly imports the same runtime file into another DB.
// Never delete these empty binding spaces or recycle a retired reader principal.
func (s *Server) bindGoodMemSubject(ctx context.Context, principal, user string) (bool, error) {
	sum := sha256.Sum256([]byte("casecite-reader-binding-v1\x00" + principal))
	sum[6], sum[8] = (sum[6]&15)|80, (sum[8]&63)|128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	space, err := s.gm.JSON(ctx, "GET", "/v1/spaces/"+id, nil)
	if goodMemStatus(err, 404) {
		base, e := s.gm.JSON(ctx, "GET", "/v1/spaces/"+s.gm.Space, nil)
		if e != nil {
			return false, e
		}
		space, err = s.gm.JSON(ctx, "POST", "/v1/spaces", M{"spaceId": id, "name": "CaseCite reader binding " + principal, "labels": M{"casecite.user": user, "casecite.reader": principal}, "spaceEmbedders": spaceEmbedderInput(base), "defaultChunkingConfig": indexChunking(nil)})
		if goodMemStatus(err, 409) {
			space, err = s.gm.JSON(ctx, "GET", "/v1/spaces/"+id, nil)
		}
	}
	if err != nil {
		return false, err
	}
	return str(object(space["labels"]), "casecite.user") == user && str(space, "ownerId") == s.cfg.GoodMemSecurity.ControllerID, nil
}

// A document owner can retain access after leaving a matter. GoodMem retrieval
// authorizes whole spaces, so sharing a space among a matter's documents would
// overgrant that owner. All representations instead use document-specific spaces.
func (s *Server) secureDocumentProfile(ctx context.Context, document string, profile M) (M, error) {
	return s.secureDocumentProfileWithTemplate(ctx, document, profile, nil)
}

func (s *Server) secureDocumentProfileWithTemplate(ctx context.Context, document string, profile, providedTemplate M) (M, error) {
	if s.cfg.GoodMemSecurity == nil {
		return profile, nil
	}
	result := M{}
	for k, v := range profile {
		result[k] = v
	}
	template := str(profile, "space_id")
	sum := sha256.Sum256([]byte("casecite-document-space-v1\x00" + s.gm.Space + "\x00" + document + "\x00" + template))
	sum[6], sum[8] = (sum[6]&15)|80, (sum[8]&63)|128
	id := fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	err := s.withGoodMemLock(ctx, func(ctx context.Context) error {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_objects o JOIN cc_users u ON u.id=o.owner_id AND (u.active OR $2) WHERE o.kind='document' AND o.id=$1 AND COALESCE(o.data->>'status','')<>'deleting')`, document, providedTemplate != nil).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return missing()
		}
		space, err := s.gm.JSON(ctx, "GET", "/v1/spaces/"+id, nil)
		if goodMemStatus(err, 404) {
			base := providedTemplate
			if base == nil {
				var e error
				base, e = s.gm.JSON(ctx, "GET", "/v1/spaces/"+template, nil)
				if e != nil {
					return e
				}
			}
			space, err = s.gm.JSON(ctx, "POST", "/v1/spaces", M{"spaceId": id, "name": "CaseCite document " + document + " " + id, "labels": M{"casecite.document": document, "casecite.base-space": s.gm.Space}, "spaceEmbedders": spaceEmbedderInput(base), "defaultChunkingConfig": indexChunking(profile)})
			if goodMemStatus(err, 409) {
				space, err = s.gm.JSON(ctx, "GET", "/v1/spaces/"+id, nil)
			}
		}
		if err != nil {
			return err
		}
		if str(space, "ownerId") != s.cfg.GoodMemSecurity.ControllerID || str(object(space["labels"]), "casecite.document") != document {
			return fail(502, "GoodMem document space has an unexpected owner or document boundary")
		}
		// No user grant exists yet. Record only a confirmed space: a failed
		// create must not leave a nonexistent resource blocking all startup ACL
		// reconciliation. An uncertain create is safe to retry by deterministic ID.
		if _, err := s.db.ExecContext(ctx, `INSERT INTO cc_goodmem_spaces(space_id,document_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, document); err != nil {
			return err
		}
		return s.reconcileGoodMemSpace(ctx, id)
	})
	result["space_id"] = id
	return result, err
}
