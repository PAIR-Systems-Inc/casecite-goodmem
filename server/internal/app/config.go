package app

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address, DatabaseURL, Secret, BootstrapToken, PublicURL string
	GoodMemURL, GoodMemKey, GoodMemSpace, RerankerID        string
	LLMURL, LLMKey, LLMModel, CourtListenerToken            string
	Debug                                                   bool
	SessionTTL, IdleTTL                                     time.Duration
	MaxUpload                                               int64
}

func LoadConfig() (Config, error) {
	c := Config{
		Address: env("LISTEN_ADDR", ":8000"), DatabaseURL: os.Getenv("DATABASE_URL"),
		Secret: os.Getenv("SECRET_KEY"), BootstrapToken: os.Getenv("REGISTRATION_BOOTSTRAP_TOKEN"),
		PublicURL:  strings.TrimRight(env("PUBLIC_URL", "http://localhost:8000"), "/"),
		GoodMemURL: strings.TrimRight(env("GOODMEM_BASE_URL", "http://goodmem:8080"), "/"),
		GoodMemKey: os.Getenv("GOODMEM_API_KEY"), GoodMemSpace: os.Getenv("GOODMEM_SPACE_ID"),
		RerankerID: os.Getenv("GOODMEM_RERANKER_ID"),
		LLMURL:     strings.TrimRight(env("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		LLMKey:     os.Getenv("OPENAI_API_KEY"), LLMModel: env("LLM_MODEL", "gpt-4.1-mini"),
		CourtListenerToken: os.Getenv("COURTLISTENER_API_TOKEN"), Debug: env("DEBUG", "false") == "true",
		SessionTTL: 8 * time.Hour, IdleTTL: 2 * time.Hour, MaxUpload: 50 << 20,
	}
	if file := os.Getenv("GOODMEM_CONFIG_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return c, err
		}
		var v struct {
			APIKey  string `json:"api_key"`
			SpaceID string `json:"space_id"`
			BaseURL string `json:"base_url"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return c, err
		}
		if c.GoodMemKey == "" {
			c.GoodMemKey = v.APIKey
		}
		if c.GoodMemSpace == "" {
			c.GoodMemSpace = v.SpaceID
		}
		if os.Getenv("GOODMEM_BASE_URL") == "" && v.BaseURL != "" {
			c.GoodMemURL = v.BaseURL
		}
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	c.DatabaseURL = strings.Replace(c.DatabaseURL, "postgresql+asyncpg://", "postgresql://", 1)
	if len(c.Secret) < 32 {
		return c, errors.New("SECRET_KEY must contain at least 32 characters")
	}
	if c.GoodMemKey == "" || c.GoodMemSpace == "" {
		return c, errors.New("GOODMEM_API_KEY and GOODMEM_SPACE_ID (or GOODMEM_CONFIG_FILE) are required")
	}
	for _, raw := range []string{c.PublicURL, c.GoodMemURL, c.LLMURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return c, errors.New("service URLs must be absolute HTTP(S) URLs")
		}
	}
	if !c.Debug && !strings.HasPrefix(c.PublicURL, "https://") {
		return c, errors.New("PUBLIC_URL must use HTTPS in production; set DEBUG=true for a local evaluation")
	}
	if n, err := strconv.ParseInt(os.Getenv("MAX_UPLOAD_MB"), 10, 64); err == nil && n > 0 && n <= 250 {
		c.MaxUpload = n << 20
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
