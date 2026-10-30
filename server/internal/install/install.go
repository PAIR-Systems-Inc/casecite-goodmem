// Package install contains dependency-free helpers used by the Docker installer.
package install

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// InitEnv never replaces a configuration: regenerating SECRET_KEY would make
// saved credentials unreadable and changing database passwords would break login.
func InitEnv(path string) error {
	values := map[string]string{
		"DEBUG": "true", "BIND_ADDRESS": "127.0.0.1", "PORT": "8000",
		"OPENAI_BASE_URL": "https://api.openai.com/v1", "LLM_MODEL": "gpt-5.4-mini",
		"OPENAI_UTILITY_MODEL": "gpt-5.4-mini", "EMBEDDING_BASE_URL": "https://api.openai.com/v1",
		"EMBEDDING_MODEL": "text-embedding-3-small", "EMBEDDING_DIMENSIONS": "1536",
		"GOODMEM_IDENTITY_CAPACITY": "64", "GOODMEM_OCR_BASE_URL": "http://ocr:8000/v1",
		"VOYAGE_BASE_URL": "https://api.voyageai.com/v1", "VOYAGE_RERANKER_MODEL": "rerank-3-lite",
		"VOYAGE_EMBEDDING_MODEL": "voyage-4-large", "VOYAGE_EMBEDDING_DIMENSIONS": "1024",
		"OCR_GPU_ID": "0",
	}
	for _, key := range []string{"OPENAI_API_KEY", "VOYAGE_API_KEY", "EMBEDDING_API_KEY", "CASECITE_OCR_CACHE"} {
		values[key] = ""
	}
	for key := range values {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			values[key] = value
		}
	}
	port, err := strconv.Atoi(values["PORT"])
	if err != nil || port < 1024 || port > 65535 {
		return errors.New("PORT must be between 1024 and 65535")
	}
	values["PUBLIC_URL"] = "http://localhost:" + values["PORT"]
	for _, key := range []string{"DB_ADMIN_PASSWORD", "CASECITE_DB_PASSWORD", "GOODMEM_DB_PASSWORD", "SECRET_KEY", "REGISTRATION_BOOTSTRAP_TOKEN"} {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		values[key] = hex.EncodeToString(b)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	content.WriteString("# CaseCite by PairSys. Keep this file private and backed up.\n# Optional SSO, connectors and other settings: see .env.example.\n")
	for _, key := range keys {
		value := values[key]
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s must be a single line", key)
		}
		// Compose expands dollar signs even inside double quotes. Do not use a
		// shell to load this file; quoting here is specifically dotenv quoting.
		escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "$$").Replace(value)
		fmt.Fprintf(&content, "%s=\"%s\"\n", key, escaped)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(f, content.String()); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return closeErr
}

// Wait checks readiness from inside the Compose network. For OCR it also
// verifies the served model name expected by GoodMem, not merely an HTTP 200.
func Wait(ctx context.Context, endpoint, model string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("readiness endpoint must be an HTTP(S) URL without credentials")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	last := "not responding"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			ready := resp.StatusCode == http.StatusOK
			last = fmt.Sprintf("HTTP %d", resp.StatusCode)
			if ready && model != "" {
				var body struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				ready = false
				if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) == nil {
					for _, entry := range body.Data {
						if entry.ID == model {
							ready = true
						}
					}
				}
				last = "required OCR model is not advertised at /v1/models"
			}
			resp.Body.Close()
			if ready {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timed out (%s): %w", last, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
