package mlaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/corazawaf/coraza/v3/collection"
	"github.com/corazawaf/coraza/v3/experimental/plugins"
	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"
)

const (
	defaultServiceURL = "http://host.docker.internal:9099/evaluate"
	defaultTimeout    = 100 * time.Millisecond
)

type mlEvaluateAction struct {
	model      string
	serviceURL string
	timeout    time.Duration
}

type evaluateRequest struct {
	Model         string         `json:"model"`
	TransactionID string         `json:"transaction_id,omitempty"`
	Features      map[string]any `json:"features"`
}

type evaluateResponse struct {
	Model string  `json:"model,omitempty"`
	Score float64 `json:"score"`
}

func init() {
	plugins.RegisterAction("mlevaluate", func() plugintypes.Action {
		return &mlEvaluateAction{}
	})
}

func (a *mlEvaluateAction) Init(_ plugintypes.RuleMetadata, data string) error {
	args, err := parseActionArgs(data)
	if err != nil {
		return err
	}

	model := args["model"]
	if model == "" {
		return errors.New("mlevaluate requires model=<name>")
	}

	serviceURL := args["url"]
	if serviceURL == "" {
		serviceURL = os.Getenv("CORAZA_ML_SERVICE_URL")
	}
	if serviceURL == "" {
		serviceURL = defaultServiceURL
	}

	timeout := defaultTimeout
	if rawTimeout := args["timeout"]; rawTimeout != "" {
		parsed, err := time.ParseDuration(rawTimeout)
		if err != nil {
			return fmt.Errorf("invalid mlevaluate timeout %q: %w", rawTimeout, err)
		}
		timeout = parsed
	}

	a.model = model
	a.serviceURL = serviceURL
	a.timeout = timeout
	return nil
}

func (a *mlEvaluateAction) Evaluate(_ plugintypes.RuleMetadata, tx plugintypes.TransactionState) {
	txVars := tx.Variables().TX()
	txVars.Set("ml_action_status", []string{"started"})
	txVars.Set("ml_service_url", []string{a.serviceURL})

	score, err := a.evaluate(tx)
	if err != nil {
		txVars.Set("ml_action_status", []string{"error"})
		txVars.Set("ml_score", []string{"0"})
		txVars.Set("ml_error", []string{err.Error()})
		return
	}

	txVars.Set("ml_action_status", []string{"ok"})
	txVars.Set("ml_score", []string{strconv.FormatFloat(score, 'f', 6, 64)})
	txVars.Set("ml_error", []string{"0"})
}

func (a *mlEvaluateAction) Type() plugintypes.ActionType {
	return plugintypes.ActionTypeNondisruptive
}

var _ plugintypes.Action = (*mlEvaluateAction)(nil)

func (a *mlEvaluateAction) evaluate(tx plugintypes.TransactionState) (float64, error) {
	payload := evaluateRequest{
		Model:         a.model,
		TransactionID: tx.ID(),
		Features:      phaseOneFeatures(tx),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodPost, a.serviceURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.Client{Timeout: a.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("ml service returned %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}

	var result evaluateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if result.Score < 0 || result.Score > 1 {
		return 0, fmt.Errorf("ml service returned score outside [0,1]: %f", result.Score)
	}
	return result.Score, nil
}

func phaseOneFeatures(tx plugintypes.TransactionState) map[string]any {
	vars := tx.Variables()
	return map[string]any{
		"method":           vars.RequestMethod().Get(),
		"request_uri":      vars.RequestURI().Get(),
		"request_uri_raw":  vars.RequestURIRaw().Get(),
		"request_filename": vars.RequestFilename().Get(),
		"query_string":     vars.QueryString().Get(),
		"request_protocol": vars.RequestProtocol().Get(),
		"remote_addr":      vars.RemoteAddr().Get(),
		"remote_port":      vars.RemotePort().Get(),
		"unique_id":        vars.UniqueID().Get(),
		"headers":          collectHeaders(vars.RequestHeaders()),
	}
}

func collectHeaders(headers collection.Map) map[string][]string {
	out := map[string][]string{}
	for _, match := range headers.FindAll() {
		key := match.Key()
		if key == "" {
			continue
		}
		out[key] = append(out[key], match.Value())
	}
	return out
}

func parseActionArgs(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return nil, errors.New("mlevaluate requires arguments")
	}

	args := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid mlevaluate argument %q", strings.TrimSpace(part))
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || value == "" {
			return nil, fmt.Errorf("invalid mlevaluate argument %q", strings.TrimSpace(part))
		}
		args[key] = value
	}
	return args, nil
}
