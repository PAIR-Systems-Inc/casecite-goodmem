package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// Only the configured space's embedders and explicitly enabled alternatives
// belong in the application catalog. Never expose upstream credentials or URLs.
func (s *Server) documentEmbeddingCatalog(ctx context.Context) (M, error) {
	space, err := s.gm.JSON(ctx, "GET", "/v1/spaces/"+url.PathEscape(s.gm.Space), nil)
	if err != nil {
		return nil, err
	}
	defaults := map[string]bool{}
	for _, entry := range maps(space["spaceEmbedders"]) {
		if id := str(entry, "embedderId"); id != "" {
			defaults[id] = true
		}
	}
	listed, err := s.gm.JSON(ctx, "GET", "/v1/embedders", nil)
	if err != nil {
		return nil, err
	}
	models, names := []M{}, []string{}
	for _, entry := range maps(listed["embedders"]) {
		id := str(entry, "embedderId")
		if id == "" || !defaults[id] && str(object(entry["labels"]), "casecite") != "enabled" {
			continue
		}
		if s.cfg.GoodMemSecurity != nil && !slices.Contains(s.cfg.GoodMemSecurity.EmbedderIDs, id) {
			continue
		}
		if str(entry, "distributionType") != "DENSE" || number(entry, "dimensionality", 0) < 1 {
			continue
		}
		text := false
		for _, modality := range stringsOf(entry["supportedModalities"]) {
			text = text || modality == "TEXT"
		}
		if !text || s.allowAIProvider(providerForEndpoint(str(entry, "endpointUrl"))) != nil {
			continue
		}
		model := str(entry, "modelIdentifier")
		models = append(models, M{"id": id, "name": fallback(str(entry, "displayName"), model), "model": model, "dimensions": entry["dimensionality"], "default": defaults[id], "provider": providerForEndpoint(str(entry, "endpointUrl"))})
		if defaults[id] {
			names = append(names, model)
		}
	}
	sort.Slice(models, func(i, j int) bool { return str(models[i], "name") < str(models[j], "name") })
	sort.Strings(names)
	return M{"models": models, "default_model": fallback(strings.Join(names, ", "), "Instance default"), "default_embedder_count": len(defaults), "chunking_unit": "characters"}, nil
}

func indexSettings(settings M) M {
	return M{"document_embedder_id": str(settings, "document_embedder_id"), "chunk_size": number(settings, "chunk_size", 1800), "chunk_overlap": number(settings, "chunk_overlap", 200)}
}

func (s *Server) describeIndexSpace(ctx context.Context, id string) (M, error) {
	space, err := s.gm.JSON(ctx, "GET", "/v1/spaces/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	models, providers := []string{}, []string{}
	for _, association := range maps(space["spaceEmbedders"]) {
		embedder, err := s.gm.JSON(ctx, "GET", "/v1/embedders/"+url.PathEscape(str(association, "embedderId")), nil)
		if err != nil {
			return nil, err
		}
		provider := providerForEndpoint(str(embedder, "endpointUrl"))
		if err = s.allowAIProvider(provider); err != nil {
			return nil, err
		}
		models = append(models, str(embedder, "modelIdentifier"))
		providers = append(providers, provider)
	}
	if len(models) == 0 {
		return nil, fail(502, "The GoodMem space has no readable embedding configuration")
	}
	return M{"model": strings.Join(models, ", "), "providers": providers}, nil
}

func (s *Server) resolveIndexProfile(ctx context.Context, settings M) (M, error) {
	profile := indexSettings(settings)
	profile["space_id"] = s.gm.Space
	profile["model"] = "Instance default"
	profile["length_measurement"] = "CHARACTER_COUNT"
	id := str(profile, "document_embedder_id")
	if id == "" {
		description, err := s.describeIndexSpace(ctx, s.gm.Space)
		if err != nil {
			return nil, err
		}
		profile["model"], profile["providers"] = description["model"], description["providers"]
		return profile, nil
	}
	catalog, err := s.documentEmbeddingCatalog(ctx)
	if err != nil {
		return nil, err
	}
	var selected M
	for _, model := range maps(catalog["models"]) {
		if str(model, "id") == id {
			selected = model
		}
	}
	if selected == nil {
		return nil, bad("The selected document embedding model is unavailable. Choose an enabled model in Settings.")
	}
	profile["model"], profile["dimensions"] = selected["model"], selected["dimensions"]
	profile["provider"] = selected["provider"]
	if selected["default"] == true && number(catalog, "default_embedder_count", 0) == 1 {
		return profile, nil
	}
	// The base-space and embedder pair has one deterministic, private space.
	// Concurrent creates or an unknown response reconcile by the same UUID;
	// models are never swapped inside a space containing existing vectors.
	sum := sha256.Sum256([]byte("casecite-index-space-v1\x00" + s.gm.Space + "\x00" + id))
	sum[6], sum[8] = (sum[6]&15)|80, (sum[8]&63)|128
	spaceID := fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	space, err := s.gm.JSON(ctx, "GET", "/v1/spaces/"+spaceID, nil)
	var remote *GoodMemError
	if errors.As(err, &remote) && remote.Status == 404 {
		space, err = s.gm.JSON(ctx, "POST", "/v1/spaces", M{"spaceId": spaceID, "name": "CaseCite index " + spaceID, "labels": M{"casecite.base-space": s.gm.Space}, "spaceEmbedders": []M{{"embedderId": id, "defaultRetrievalWeight": 1}}, "defaultChunkingConfig": indexChunking(profile)})
		if errors.As(err, &remote) && remote.Status == 409 {
			space, err = s.gm.JSON(ctx, "GET", "/v1/spaces/"+spaceID, nil)
		}
	}
	if err != nil {
		return nil, err
	}
	embedders := maps(space["spaceEmbedders"])
	if str(space, "spaceId") != spaceID || space["publicRead"] == true || len(embedders) != 1 || str(embedders[0], "embedderId") != id || str(object(space["labels"]), "casecite.base-space") != s.gm.Space {
		return nil, fail(502, "The GoodMem index space does not match the selected embedding model")
	}
	profile["space_id"] = spaceID
	return profile, nil
}

func indexChunking(profile M) M {
	return M{"recursive": M{"chunkSize": number(profile, "chunk_size", 1800), "chunkOverlap": number(profile, "chunk_overlap", 200), "lengthMeasurement": "CHARACTER_COUNT", "keepStrategy": "KEEP_END"}}
}

func (g *GoodMem) withIndexProfile(profile M) *GoodMem {
	copy := *g
	copy.Space = fallback(str(profile, "space_id"), g.Space)
	copy.Chunking = indexChunking(profile)
	return &copy
}

func (s *Server) searchDocumentIndexes(ctx context.Context, query string, docs []M, k int, rerank bool) ([]Hit, error) {
	ids, spaces := []string{}, map[string]string{}
	checked := map[string]bool{}
	for _, doc := range docs {
		if provider := str(object(doc["index_profile"]), "provider"); provider != "" {
			if err := s.allowAIProvider(provider); err != nil {
				return nil, err
			}
		}
		id := documentSearchMemory(doc)
		if id == "" {
			continue
		}
		ids = append(ids, id)
		spaces[id] = fallback(str(object(doc["index_profile"]), "space_id"), s.gm.Space)
		if s.cfg.ProviderPolicyEnabled && !checked[spaces[id]] {
			// Check the current endpoint, including legacy documents and the
			// instance default. An operator may have edited an embedder since
			// ingestion; a stored provider label is not an authorization grant.
			if _, err := s.describeIndexSpace(ctx, spaces[id]); err != nil {
				return nil, err
			}
			checked[spaces[id]] = true
		}
	}
	return s.gm.searchWithSpaces(ctx, query, ids, spaces, k, rerank)
}
