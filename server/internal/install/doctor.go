package install

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type doctorReport struct {
	out                      io.Writer
	passed, warnings, failed int
}

func (d *doctorReport) mark(level, message string) {
	switch level {
	case "PASS":
		d.passed++
	case "WARN":
		d.warnings++
	case "FAIL":
		d.failed++
	}
	fmt.Fprintf(d.out, "%-4s  %s\n", level, message)
}

func (d *doctorReport) check(name string, err error, fix string) {
	if err != nil {
		d.mark("FAIL", name+": "+err.Error()+". "+fix)
	} else {
		d.mark("PASS", name)
	}
}

func doctorEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Doctor is deliberately separate from app startup: no migrations, workers,
// provisioning, document access or credential writes. Only --probe-providers
// sends synthetic text to providers; it can incur small API charges.
func Doctor(ctx context.Context, out io.Writer, probe bool) error {
	d := &doctorReport{out: out}
	fmt.Fprintln(out, "Runtime checks (inside the existing application container)")
	d.check("PostgreSQL connection", doctorDatabase(ctx, os.Getenv("DATABASE_URL")), "Check the postgres service and saved database password; do not regenerate secrets.")
	var ready struct {
		Ready bool `json:"ready"`
	}
	err := doctorRequest(ctx, http.MethodGet, "http://127.0.0.1:8000/ready", nil, nil, &ready)
	if err == nil && !ready.Ready {
		err = errors.New("application is not ready")
	}
	d.check("Application readiness", err, "Inspect app and goodmem logs with bash scripts/casecite.sh logs.")
	d.check("Scoped GoodMem access", doctorGoodMem(ctx), "Check goodmem and the runtime connection volume; preserve the existing scoped configuration.")
	d.check("OCR model pairsys/pairsys.ocr", doctorOCR(ctx, os.Getenv("GOODMEM_OCR_BASE_URL")), "Check the OCR service, model name and container-to-OCR connectivity.")
	if !probe {
		d.mark("WARN", "Provider requests skipped. Use doctor --probe-providers to verify credentials/models with small, potentially billable requests.")
	}
	openAIKey := os.Getenv("OPENAI_API_KEY")
	embedKey := doctorEnv("EMBEDDING_API_KEY", openAIKey)
	if openAIKey == "" {
		d.mark("FAIL", "Generation key is missing. Set OPENAI_API_KEY in the private .env and run start to apply it.")
	} else if probe {
		d.check("Generation provider", doctorGeneration(ctx, doctorEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"), openAIKey, doctorEnv("LLM_MODEL", "gpt-5.4-mini")), "Check OPENAI_API_KEY, LLM_MODEL, endpoint and provider quota.")
	} else {
		d.mark("PASS", "Generation key is present (validity not tested).")
	}
	if embedKey == "" {
		d.mark("FAIL", "Embedding key is missing. Check EMBEDDING_API_KEY or OPENAI_API_KEY.")
	} else if probe {
		d.check("Embedding provider and dimensions", doctorEmbedding(ctx, doctorEnv("EMBEDDING_BASE_URL", "https://api.openai.com/v1"), embedKey, doctorEnv("EMBEDDING_MODEL", "text-embedding-3-small"), doctorEnv("EMBEDDING_DIMENSIONS", "1536"), false), "Check the embedding key, model and dimensions; stored GoodMem models must use the same configuration.")
	} else {
		d.mark("PASS", "Embedding key is present (validity not tested).")
	}
	if key := os.Getenv("VOYAGE_API_KEY"); key == "" {
		d.mark("WARN", "Voyage is not configured. Optional Voyage embedding and reranking are unavailable.")
	} else if probe {
		base := doctorEnv("VOYAGE_BASE_URL", "https://api.voyageai.com/v1")
		d.check("Voyage embeddings and dimensions", doctorEmbedding(ctx, base, key, doctorEnv("VOYAGE_EMBEDDING_MODEL", "voyage-4-large"), doctorEnv("VOYAGE_EMBEDDING_DIMENSIONS", "1024"), true), "Check VOYAGE_API_KEY, embedding model, dimensions and quota.")
		d.check("Voyage reranking", doctorRerank(ctx, base, key, doctorEnv("VOYAGE_RERANKER_MODEL", "rerank-3-lite")), "Check VOYAGE_API_KEY, reranker model and quota.")
	} else {
		d.mark("PASS", "Voyage key is present (validity not tested).")
	}
	fmt.Fprintf(out, "Runtime: %d passed, %d warnings, %d failures.\n", d.passed, d.warnings, d.failed)
	if d.failed > 0 {
		return errors.New("runtime diagnostics found blocking failures")
	}
	return nil
}

func doctorDatabase(ctx context.Context, dsn string) error {
	if dsn == "" {
		return errors.New("database configuration is missing")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("database configuration is invalid")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if db.PingContext(ctx) != nil {
		return errors.New("database connection failed")
	}
	return nil
}

func doctorGoodMem(ctx context.Context) error {
	var config struct {
		Version    int    `json:"security_version"`
		Key        string `json:"api_key"`
		Space      string `json:"space_id"`
		Base       string `json:"base_url"`
		Controller string `json:"controller_id"`
	}
	f, err := os.Open(os.Getenv("GOODMEM_CONFIG_FILE"))
	if err != nil {
		return errors.New("runtime configuration is unreadable")
	}
	defer f.Close()
	if json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&config) != nil || config.Version != 1 || config.Key == "" || config.Space == "" || config.Controller == "" {
		return errors.New("scoped runtime configuration is invalid")
	}
	base := doctorEnv("GOODMEM_BASE_URL", config.Base)
	// Never send the scoped credential to an endpoint different from provisioning.
	if strings.TrimRight(base, "/") != strings.TrimRight(config.Base, "/") {
		return errors.New("GoodMem endpoint differs from the saved runtime configuration")
	}
	var space map[string]any
	return doctorRequest(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/spaces/"+url.PathEscape(config.Space), http.Header{"X-Api-Key": []string{config.Key}}, nil, &space)
}

func doctorOCR(ctx context.Context, base string) error {
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doctorRequest(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/models", nil, nil, &result); err != nil {
		return err
	}
	for _, model := range result.Data {
		if model.ID == "pairsys/pairsys.ocr" {
			return nil
		}
	}
	return errors.New("required model is not advertised at /v1/models")
}

func doctorAuth(key string) http.Header {
	return http.Header{"Authorization": []string{"Bearer " + key}}
}

func doctorEmbedding(ctx context.Context, base, key, model, dimensions string, voyage bool) error {
	dims, err := strconv.Atoi(dimensions)
	if err != nil || dims < 1 || dims > 65536 {
		return errors.New("invalid configured embedding dimensions")
	}
	payload := map[string]any{"model": model, "input": []string{"CaseCite setup diagnostic."}}
	if voyage {
		payload["output_dimension"] = dims
		payload["input_type"] = "document"
	} else if strings.HasPrefix(model, "text-embedding-3") {
		payload["dimensions"] = dims
	}
	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err = doctorRequest(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/embeddings", doctorAuth(key), payload, &result); err != nil {
		return err
	}
	if len(result.Data) != 1 || len(result.Data[0].Embedding) != dims {
		return errors.New("provider returned unexpected embedding dimensions")
	}
	return nil
}

func doctorGeneration(ctx context.Context, base, key, model string) error {
	payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": "Reply with OK. This is a setup diagnostic."}}}
	budget := "max_tokens"
	if strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4") {
		budget = "max_completion_tokens"
	}
	payload[budget] = 512
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := doctorRequest(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/chat/completions", doctorAuth(key), payload, &result); err != nil {
		return err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return errors.New("provider returned no text within the diagnostic token budget")
	}
	return nil
}

func doctorRerank(ctx context.Context, base, key, model string) error {
	var result struct {
		Data []struct {
			Index int      `json:"index"`
			Score *float64 `json:"relevance_score"`
		} `json:"data"`
	}
	payload := map[string]any{"model": model, "query": "notice period", "documents": []string{"The notice period is thirty days."}, "top_k": 1}
	if err := doctorRequest(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/rerank", doctorAuth(key), payload, &result); err != nil {
		return err
	}
	if len(result.Data) != 1 || result.Data[0].Index != 0 || result.Data[0].Score == nil {
		return errors.New("provider returned an invalid reranking result")
	}
	return nil
}

// Never include request URLs, headers, response bodies or raw transport errors
// in a diagnostic: each can contain credentials. Never follow redirects.
func doctorRequest(ctx context.Context, method, endpoint string, headers http.Header, body, result any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid endpoint configuration")
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return errors.New("invalid diagnostic request")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid diagnostic request")
	}
	if headers != nil {
		req.Header = headers.Clone()
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("connection failed or timed out (check DNS, TLS and network access)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 401, 403:
			return fmt.Errorf("authorization rejected (HTTP %d)", resp.StatusCode)
		case 429:
			return errors.New("provider quota or rate limit reached (HTTP 429)")
		default:
			return fmt.Errorf("endpoint returned HTTP %d", resp.StatusCode)
		}
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(result) != nil {
		return errors.New("endpoint returned invalid JSON")
	}
	return nil
}
