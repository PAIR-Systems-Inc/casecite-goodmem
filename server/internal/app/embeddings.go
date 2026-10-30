package app

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
)

type embeddingRoute struct {
	Provider, Kind, Endpoint, Model, Key string
}

func (s *Server) clauseEmbeddingRoute(ctx context.Context, user string) (embeddingRoute, error) {
	route := embeddingRoute{Provider: "openai", Kind: "openai", Endpoint: "https://api.openai.com/v1"}
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return route, err
	}
	route.Model = fallback(str(settings, "embedding_model"), "text-embedding-3-small")
	// A custom embedding server has its own credential and never receives a
	// personal cloud key. The chat endpoint is deliberately independent.
	if s.cfg.EmbeddingURL != "" && providerForEndpoint(s.cfg.EmbeddingURL) != "openai" {
		route.Endpoint, err = validateLLMEndpoint(s.cfg.EmbeddingURL)
		if err != nil {
			return route, err
		}
		route.Model = fallback(s.cfg.EmbeddingModel, route.Model)
		route.Provider, route.Key = providerForEndpoint(route.Endpoint), s.cfg.EmbeddingKey
		return route, s.allowAIProvider(route.Provider)
	}
	keys := map[string]string{}
	for name, fallbackKey := range map[string]string{"openai": fallback(s.cfg.EmbeddingKey, s.cfg.LLMKey), "voyage": s.cfg.VoyageKey, "cohere": s.cfg.CohereKey} {
		keys[name], err = s.providerKey(ctx, user, name, fallbackKey)
		if err != nil {
			return route, err
		}
	}
	if !strings.HasPrefix(route.Model, "voyage") && keys["voyage"] != "" && keys["openai"] == "" {
		route.Model = "voyage-law-2"
	} else if !strings.HasPrefix(route.Model, "embed-") && keys["cohere"] != "" && keys["openai"] == "" && keys["voyage"] == "" {
		route.Model = "embed-english-v3.0"
	}
	if strings.HasPrefix(route.Model, "voyage") {
		route.Provider, route.Kind, route.Endpoint = "voyage", "voyage", "https://api.voyageai.com/v1"
	}
	if strings.HasPrefix(route.Model, "embed-") {
		route.Provider, route.Kind, route.Endpoint = "cohere", "cohere", "https://api.cohere.ai/v1"
	}
	route.Key = keys[route.Kind]
	if route.Key == "" {
		return route, bad("Configure a " + route.Provider + " embedding key in Settings.")
	}
	return route, s.allowAIProvider(route.Provider)
}

func (s *Server) contractEmbeddings(ctx context.Context, user string, texts []string) ([][]float64, error) {
	route, err := s.clauseEmbeddingRoute(ctx, user)
	if err != nil {
		return nil, err
	}
	return s.contractEmbeddingsAt(ctx, route, texts)
}

func (s *Server) contractEmbeddingsAt(ctx context.Context, route embeddingRoute, texts []string) ([][]float64, error) {
	result := [][]float64{}
	dimensions := 0
	for start := 0; start < len(texts); start += 64 {
		batch := texts[start:min(start+64, len(texts))]
		payload := M{"model": route.Model, "input": batch}
		path := "/embeddings"
		switch route.Kind {
		case "voyage":
			payload["input_type"] = "document"
		case "cohere":
			payload = M{"model": route.Model, "texts": batch, "input_type": "search_document", "truncate": "END"}
			path = "/embed"
		}
		headers := http.Header{"Content-Type": []string{"application/json"}}
		if route.Key != "" {
			headers.Set("Authorization", "Bearer "+route.Key)
		}
		data, err := s.modelRequest(ctx, route.Provider, route.Endpoint+path, headers, payload, false, 32<<20)
		if err != nil {
			return nil, err
		}
		var rawVectors [][]*float64
		if route.Kind == "cohere" {
			if json.Unmarshal(jsonBytes(data["embeddings"]), &rawVectors) != nil {
				return nil, fail(502, "Invalid clause embedding response")
			}
		} else {
			var decoded struct {
				Data []struct {
					Index     *int       `json:"index"`
					Embedding []*float64 `json:"embedding"`
				} `json:"data"`
			}
			if json.Unmarshal(jsonBytes(data), &decoded) != nil {
				return nil, fail(502, "Invalid clause embedding response")
			}
			if len(decoded.Data) != len(batch) {
				return nil, fail(502, "Clause embedding provider omitted a segment")
			}
			rawVectors = make([][]*float64, len(batch))
			for _, item := range decoded.Data {
				if item.Index == nil || *item.Index < 0 || *item.Index >= len(batch) || rawVectors[*item.Index] != nil {
					return nil, fail(502, "Invalid clause embedding order")
				}
				rawVectors[*item.Index] = item.Embedding
			}
		}
		if len(rawVectors) != len(batch) {
			return nil, fail(502, "Clause embedding provider omitted a segment")
		}
		vectors := make([][]float64, len(rawVectors))
		for i, vector := range rawVectors {
			if dimensions == 0 {
				dimensions = len(vector)
			}
			if len(vector) == 0 || len(vector) != dimensions {
				return nil, fail(502, "Clause embedding dimensions do not match")
			}
			for _, value := range vector {
				if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
					return nil, fail(502, "Invalid clause embedding value")
				}
				vectors[i] = append(vectors[i], *value)
			}
		}
		result = append(result, vectors...)
	}
	return result, nil
}
