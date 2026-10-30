package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (s *Server) llm(ctx context.Context, user, system, prompt string, jsonMode bool) (string, M, error) {
	selected, err := s.selectLLM(ctx, user)
	if err != nil {
		return "", nil, err
	}
	if jsonMode {
		system += "\nReturn one valid JSON object, without Markdown fences."
	}
	endpoint, headers, payload := generationRequest(selected, system, prompt, jsonMode)
	data, err := s.modelRequest(ctx, selected.Provider, endpoint, headers, payload, selected.Protocol == "openai", 4<<20)
	if err != nil {
		return "", nil, err
	}
	answer, usage, err := generationResponse(selected.Protocol, data)
	if err != nil {
		return "", nil, err
	}
	return answer, M{"model": selected.Model, "provider": selected.Provider, "usage": usage}, nil
}

func generationRequest(selected llmSelection, system, prompt string, jsonMode bool) (string, http.Header, M) {
	headers := http.Header{"Content-Type": []string{"application/json"}}
	switch selected.Protocol {
	case "anthropic":
		headers.Set("Authorization", "Bearer "+selected.Key)
		headers.Set("anthropic-version", "2026-10-21")
		// This API version no longer accepts temperature/top_p/top_k.
		return selected.Endpoint + "/messages", headers, M{"model": selected.Model, "max_tokens": selected.MaxTokens, "system": []M{{"type": "text", "text": system}}, "messages": []M{{"role": "user", "content": prompt}}}
	case "google":
		headers.Set("x-goog-api-key", selected.Key)
		config := M{"maxOutputTokens": selected.MaxTokens, "temperature": selected.Temperature}
		if jsonMode {
			config["responseMimeType"] = "application/json"
		}
		model := strings.TrimPrefix(selected.Model, "models/")
		return selected.Endpoint + "/models/" + url.PathEscape(model) + ":generateContent", headers, M{"systemInstruction": M{"parts": []M{{"text": system}}}, "contents": []M{{"role": "user", "parts": []M{{"text": prompt}}}}, "generationConfig": config}
	default:
		if selected.Key != "" {
			headers.Set("Authorization", "Bearer "+selected.Key)
		}
		payload := M{"model": selected.Model, "messages": []M{{"role": "system", "content": system}, {"role": "user", "content": prompt}}, "max_tokens": selected.MaxTokens, "temperature": selected.Temperature}
		if jsonMode {
			payload["response_format"] = M{"type": "json_object"}
		}
		return selected.Endpoint + "/chat/completions", headers, payload
	}
}

// Only explicit unsupported-parameter complaints are negotiated. Never remove
// token budgets, retry authentication failures, or expose provider bodies.
func adaptModelParameters(payload M, response []byte) bool {
	var decoded M
	if json.Unmarshal(response, &decoded) != nil {
		return false
	}
	err := object(decoded["error"])
	message := strings.ToLower(str(err, "message"))
	param := str(err, "param")
	if !strings.Contains(message, "unsupported") && !strings.Contains(message, "not support") && !strings.Contains(message, "unrecognized") && !strings.Contains(message, "unknown parameter") {
		return false
	}
	for _, key := range []string{"max_tokens", "max_completion_tokens", "temperature", "response_format"} {
		if _, ok := payload[key]; !ok {
			continue
		}
		if param != key && !strings.Contains(message, key) {
			continue
		}
		switch key {
		case "max_tokens":
			payload["max_completion_tokens"] = payload[key]
		case "max_completion_tokens":
			payload["max_tokens"] = payload[key]
		}
		delete(payload, key)
		return true
	}
	return false
}

func modelRetryDelay(header string, attempt int) time.Duration {
	delay := time.Duration(1<<attempt) * 250 * time.Millisecond
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, 10) * float64(time.Second))
	}
	if when, err := http.ParseTime(header); err == nil {
		return min(max(time.Until(when), 0), 10*time.Second)
	}
	return delay
}

func (s *Server) modelRequest(ctx context.Context, provider, endpoint string, headers http.Header, payload M, negotiate bool, limit int64) (M, error) {
	if err := s.allowAIProvider(provider); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	retries, adaptations := 0, 0
	client := *s.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBytes(payload)))
		if err != nil {
			return nil, bad("The model provider endpoint is invalid.")
		}
		req.Header = headers.Clone()
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if retries < 2 {
				timer := time.NewTimer(modelRetryDelay("", retries))
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
				retries++
				continue
			}
			return nil, fail(502, "The model provider could not be reached. Check its endpoint and retry.")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fail(502, "The model provider response was interrupted. No partial output was saved.")
		}
		if int64(len(body)) > limit {
			return nil, fail(502, "The model provider response exceeded the supported size.")
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var result M
			if json.Unmarshal(body, &result) != nil || result == nil {
				return nil, fail(502, "The model provider returned an invalid response.")
			}
			return result, nil
		}
		if negotiate && resp.StatusCode == 400 && adaptations < 3 && adaptModelParameters(payload, body) {
			adaptations++
			continue
		}
		if retries < 2 && (resp.StatusCode == 429 || resp.StatusCode == 408 || resp.StatusCode >= 500) {
			timer := time.NewTimer(modelRetryDelay(resp.Header.Get("Retry-After"), retries))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			retries++
			continue
		}
		return nil, fail(502, fmt.Sprintf("The %s model provider returned HTTP %d. Check its key, model name and account limits.", provider, resp.StatusCode))
	}
}

func generationResponse(protocol string, data M) (string, M, error) {
	answer, finish := "", ""
	usage := object(data["usage"])
	refused := false
	switch protocol {
	case "anthropic":
		finish = str(data, "stop_reason")
		refused = str(object(data["stop_details"]), "type") == "refusal"
		for _, block := range maps(data["content"]) {
			if str(block, "type") == "text" {
				answer += str(block, "text")
			}
			if str(block, "type") == "refusal" || str(block, "type") == "tool_use" {
				refused = true
			}
		}
		refused = refused || (finish != "end_turn" && finish != "stop_sequence" && finish != "max_tokens")
	case "google":
		usage = object(data["usageMetadata"])
		candidates := maps(data["candidates"])
		if len(candidates) > 0 {
			candidate := candidates[0]
			finish = str(candidate, "finishReason")
			for _, part := range maps(object(candidate["content"])["parts"]) {
				if part["thought"] != true {
					answer += str(part, "text")
				}
				if part["functionCall"] != nil {
					refused = true
				}
			}
		}
		refused = refused || (finish != "STOP" && finish != "MAX_TOKENS") || str(object(data["promptFeedback"]), "blockReason") != ""
	default:
		choices := maps(data["choices"])
		if len(choices) > 0 {
			choice := choices[0]
			message := object(choice["message"])
			answer, finish = str(message, "content"), str(choice, "finish_reason")
			refused = str(message, "refusal") != "" || len(maps(message["tool_calls"])) > 0 || message["function_call"] != nil
		}
		refused = refused || (finish != "" && finish != "stop" && finish != "length")
	}
	if finish == "length" || finish == "max_tokens" || finish == "MAX_TOKENS" {
		return "", nil, bad("The model reached its output limit. Increase the output budget or narrow the request; no partial analysis was saved.")
	}
	if refused || strings.TrimSpace(answer) == "" {
		return "", nil, bad("The model could not complete this request. No partial analysis was saved.")
	}
	return answer, usage, nil
}
