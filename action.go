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
	defaultServiceURL = "http://127.0.0.1:9099/evaluate"
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

	score, err := a.evaluate(tx)
	if err != nil {
		txVars.Set("ml_score", []string{"0"})
		txVars.Set("ml_error", []string{err.Error()})
		return
	}

	txVars.Set("ml_score", []string{strconv.FormatFloat(score, 'f', 6, 64)})
	txVars.Set("ml_error", []string{"0"})
}

func (a *mlEvaluateAction) Type() plugintypes.ActionType {
	return plugintypes.ActionTypeNondisruptive
}

var _ plugintypes.Action = (*mlEvaluateAction)(nil)