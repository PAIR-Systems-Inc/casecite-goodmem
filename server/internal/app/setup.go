package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// SetupGoodMem is an explicit administrative command, never run at API startup.
// Persist each resource as it is created so a retry reuses it and never loses
// the one-time root credential returned by initialization.
func setupGoodMemAdmin(ctx context.Context, output string) error {
	state := M{}
	if b, err := os.ReadFile(output); err == nil {
		if err = json.Unmarshal(b, &state); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	base := env("GOODMEM_BASE_URL", "http://localhost:8080")
	if stored := str(state, "base_url"); stored != "" && stored != base {
		return errors.New("setup file belongs to a different GoodMem URL")
	}
	state["base_url"] = base
	if !safeHTTPURL(base) {
		return errors.New("GOODMEM_BASE_URL must be an absolute HTTP(S) URL")
	}
	save := func() error {
		if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
			return err
		}
		b, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(output), ".goodmem-*.json")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		if _, err = f.Write(b); err != nil {
			f.Close()
			return err
		}
		if err = f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		return os.Rename(name, output)
	}
	g := NewGoodMem(Config{GoodMemURL: base, GoodMemKey: env("GOODMEM_API_KEY", str(state, "api_key"))})
	if g.Key == "" {
		if os.Getenv("GOODMEM_INITIALIZE") != "true" {
			return errors.New("set GOODMEM_API_KEY for an existing server or GOODMEM_INITIALIZE=true to initialize a new server")
		}
		m, err := g.JSON(ctx, "POST", "/v1/system/init", M{})
		if err != nil {
			return err
		}
		g.Key = str(m, "rootApiKey")
		if g.Key == "" {
			return errors.New("GoodMem is already initialized; provide its administrative GOODMEM_API_KEY")
		}
		state["api_key"] = g.Key
		if err = save(); err != nil {
			return fmt.Errorf("could not save the newly issued GoodMem credential: %w", err)
		}
	} else {
		state["api_key"] = g.Key
		if err := save(); err != nil {
			return err
		}
	}
	if id := env("GOODMEM_SPACE_ID", str(state, "space_id")); id != "" {
		g.Space = id
		if err := g.Check(ctx); err != nil {
			return err
		}
		state["space_id"] = id
		return save()
	}
	embedder := env("GOODMEM_EMBEDDER_ID", str(state, "embedder_id"))
	if embedder == "" {
		dims, err := strconv.Atoi(env("EMBEDDING_DIMENSIONS", "1536"))
		if err != nil || dims < 1 {
			return errors.New("invalid EMBEDDING_DIMENSIONS")
		}
		model := env("EMBEDDING_MODEL", "text-embedding-3-small")
		payload := M{"displayName": "CaseCite " + model, "providerType": env("EMBEDDING_PROVIDER", "OPENAI"), "endpointUrl": env("EMBEDDING_BASE_URL", "https://api.openai.com/v1"), "modelIdentifier": model, "dimensionality": dims, "distributionType": "DENSE", "supportedModalities": []string{"TEXT"}}
		if key := env("EMBEDDING_API_KEY", os.Getenv("OPENAI_API_KEY")); key != "" {
			payload["credentials"] = M{"kind": "CREDENTIAL_KIND_API_KEY", "apiKey": M{"inlineSecret": key}}
		}
		m, err := g.JSON(ctx, "POST", "/v1/embedders", payload)
		if err != nil {
			return err
		}
		embedder = str(m, "embedderId")
		if embedder == "" {
			return errors.New("GoodMem omitted the embedder ID")
		}
		state["embedder_id"] = embedder
		if err = save(); err != nil {
			return err
		}
	}
	m, err := g.JSON(ctx, "POST", "/v1/spaces", M{"name": env("GOODMEM_SPACE_NAME", "CaseCite"), "spaceEmbedders": []M{{"embedderId": embedder, "defaultRetrievalWeight": 1}}, "defaultChunkingConfig": M{"recursive": M{"chunkSize": 1800, "chunkOverlap": 200, "lengthMeasurement": "CHARACTER_COUNT", "keepStrategy": "KEEP_END"}}})
	if err != nil {
		return err
	}
	state["space_id"] = str(m, "spaceId")
	if str(state, "space_id") == "" {
		return errors.New("GoodMem omitted the space ID")
	}
	return save()
}

func SetupWithTimeout(output string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return SetupGoodMem(ctx, output)
}
