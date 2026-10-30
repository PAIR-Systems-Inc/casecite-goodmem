package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// setupModels is opt-in for the portable installer. Persist client-selected IDs
// before creating resources, so a crash or lost POST response cannot duplicate
// providers on retry. Never rotate existing credentials as a startup side effect.
func setupModels(ctx context.Context, g *GoodMem, state M, save func() error) error {
	credential := func(key string) M {
		return M{"kind": "CREDENTIAL_KIND_API_KEY", "apiKey": M{"inlineSecret": key}}
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		model := env("LLM_MODEL", "gpt-5.4-mini")
		payload := M{"displayName": "CaseCite " + model, "providerType": "OPENAI", "endpointUrl": env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "modelIdentifier": model, "supportedModalities": []string{"TEXT"}, "credentials": credential(key)}
		if err := ensureSetupModel(ctx, g, state, save, "llm_id", "llms", "llmId", payload); err != nil {
			return err
		}
	}
	if key := os.Getenv("VOYAGE_API_KEY"); key != "" {
		base := env("VOYAGE_BASE_URL", "https://api.voyageai.com/v1")
		if os.Getenv("GOODMEM_RERANKER_ID") == "" {
			model := env("VOYAGE_RERANKER_MODEL", "rerank-3-lite")
			payload := M{"displayName": "CaseCite " + model, "providerType": "VOYAGE", "endpointUrl": base, "apiPath": "/rerank", "modelIdentifier": model, "supportedModalities": []string{"TEXT"}, "credentials": credential(key)}
			if err := ensureSetupModel(ctx, g, state, save, "reranker_id", "rerankers", "rerankerId", payload); err != nil {
				return err
			}
		}
		dims, err := strconv.Atoi(env("VOYAGE_EMBEDDING_DIMENSIONS", "1024"))
		if err != nil || dims < 1 {
			return errors.New("invalid VOYAGE_EMBEDDING_DIMENSIONS")
		}
		model := env("VOYAGE_EMBEDDING_MODEL", "voyage-4-large")
		payload := M{"displayName": "CaseCite " + model, "providerType": "VOYAGE", "endpointUrl": base, "modelIdentifier": model, "dimensionality": dims, "distributionType": "DENSE", "supportedModalities": []string{"TEXT"}, "credentials": credential(key)}
		if err := ensureSetupModel(ctx, g, state, save, "voyage_embedder_id", "embedders", "embedderId", payload); err != nil {
			return err
		}
	}
	return nil
}

func ensureSetupModel(ctx context.Context, g *GoodMem, state M, save func() error, stateKey, collection, idKey string, payload M) error {
	id := str(state, stateKey)
	if id == "" {
		id = uuid()
		state[stateKey] = id
		if err := save(); err != nil {
			return err
		}
	}
	resource, err := g.JSON(ctx, "GET", "/v1/"+collection+"/"+id, nil)
	if goodMemStatus(err, 404) {
		payload[idKey] = id
		if _, err = g.JSON(ctx, "POST", "/v1/"+collection, payload); err != nil {
			return err
		}
		// LLM creation returns a wrapper, unlike embedder/reranker creation.
		// Canonical GET also verifies the resource persisted with the chosen ID.
		resource, err = g.JSON(ctx, "GET", "/v1/"+collection+"/"+id, nil)
	}
	if err != nil {
		return err
	}
	for _, key := range []string{idKey, "providerType", "endpointUrl", "modelIdentifier"} {
		expected := str(payload, key)
		if key == idKey {
			expected = id
		}
		if str(resource, key) != expected {
			return fmt.Errorf("saved %s differs from requested %s; configure model changes explicitly instead of replacing an existing provider", stateKey, key)
		}
	}
	return nil
}
