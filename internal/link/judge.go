package link

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	jevEndpoint      = "https://api.typesafe.ai/v1/systemone"
	jevModel         = "jev-latest"
	maxStateChars    = 12000
	sameInstructions = "Session A and session B belong to the same piece of work: the same feature, fix, document, investigation or decision. Working in the same repository or on the same broad topic is not enough."
)

type Verdict struct {
	Probability float64
	InputTokens int64
}

type Judge interface {
	Same(ctx context.Context, cardA, cardB string) (Verdict, error)
}

type JEV struct {
	Endpoint, Model, Key string
	HTTP                 *http.Client
}

func NewJEV(key string) *JEV {
	return &JEV{Endpoint: jevEndpoint, Model: jevModel, Key: key, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (j *JEV) Same(ctx context.Context, a, b string) (Verdict, error) {
	state := "Session A:\n" + a + "\nSession B:\n" + b
	if len(state) > maxStateChars {
		state = state[:maxStateChars]
	}
	body, _ := json.Marshal(map[string]any{
		"model": j.Model,
		"state": state,
		"questions": map[string]any{
			"same": map[string]any{"type": "noul", "instructions": sameInstructions},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Verdict{}, err
	}
	req.Header.Set("Authorization", "Bearer "+j.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := j.HTTP.Do(req)
	if err != nil {
		if inner := errors.Unwrap(err); inner != nil {
			err = inner
		}
		return Verdict{}, fmt.Errorf("jev: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Verdict{}, fmt.Errorf("jev: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Answers map[string]struct {
			Noul *float64 `json:"noul"`
		} `json:"answers"`
		Usage struct {
			InputTokens int64 `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Verdict{}, fmt.Errorf("jev: bad response: %w", err)
	}
	ans, ok := out.Answers["same"]
	if !ok || ans.Noul == nil {
		return Verdict{}, errors.New("jev: no answer for question 'same'")
	}
	return Verdict{Probability: *ans.Noul, InputTokens: out.Usage.InputTokens}, nil
}

func LoadKey(hermesHome string) string {
	if k := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY")); k != "" {
		return k
	}
	data, err := os.ReadFile(filepath.Join(hermesHome, ".env"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(name) == "TYPESAFE_API_KEY" {
			return strings.Trim(strings.TrimSpace(value), `'"`)
		}
	}
	return ""
}

func PairHash(hashA, hashB string) string {
	if hashA > hashB {
		hashA, hashB = hashB, hashA
	}
	sum := sha256.Sum256([]byte(hashA + "|" + hashB))
	return hex.EncodeToString(sum[:16])
}
