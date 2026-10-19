package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Elicitation, transport-neutral.
//
// A handler that needs the user's input calls elicitValues(ctx, …). Who
// answers depends on the request:
//
//   - a modern (2026-07-28) client that declared the elicitation capability:
//     the question goes back as an InputRequiredResult (Multi Round-Trip
//     Requests). The handler stays suspended in this process; the result's
//     requestState is an opaque, single-use handle to it, and the client's
//     retry of tools/call with inputResponses + requestState resumes it.
//     Login asks up to six questions with a browser OAuth flow in between, so
//     re-running the handler from scratch on every retry is not an option —
//     the handle is the explicit cross-call state the spec allows in place of
//     a session.
//   - a legacy (2025-03-26) stdio client that declared elicitation in
//     initialize: the old server-initiated elicitation/create request.
//   - anyone else: no elicitor, clientCanElicit reports false and handlers
//     fall back to returning instructions as text.

// elicitTimeout bounds how long a handler waits for one answer.
const elicitTimeout = 10 * time.Minute

// elicitor asks the user for values matching schema and returns the filled
// content and the action ("accept", "decline" or "cancel").
type elicitor interface {
	elicit(ctx context.Context, message string, schema map[string]interface{}) (map[string]interface{}, string, error)
}

const elicitorContextKey contextKey = "mcpElicitor"

func withElicitor(ctx context.Context, e elicitor) context.Context {
	return context.WithValue(ctx, elicitorContextKey, e)
}

// clientCanElicit reports whether the caller of this request can answer
// elicitation.
func clientCanElicit(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	e, _ := ctx.Value(elicitorContextKey).(elicitor)
	return e != nil
}

// elicitValues asks the caller of this request for input. See the file
// comment for how the question reaches the user.
func elicitValues(ctx context.Context, message string, schema map[string]interface{}) (content map[string]interface{}, action string, err error) {
	e, _ := ctx.Value(elicitorContextKey).(elicitor)
	if e == nil {
		return nil, "", fmt.Errorf("client does not support elicitation")
	}
	return e.elicit(ctx, message, schema)
}

// elicitResult is the client's ElicitResult.
type elicitResult struct {
	Action  string                 `json:"action"`
	Content map[string]interface{} `json:"content"`
}

// ---- legacy stdio: server-initiated elicitation/create ----------------------

// stdioElicitor sends elicitation/create to a legacy stdio client and waits
// for the response, routed back by runMCPServer through pendingReqs.
type stdioElicitor struct{}

func (stdioElicitor) elicit(ctx context.Context, message string, schema map[string]interface{}) (map[string]interface{}, string, error) {
	id := fmt.Sprintf("elicit-%d", nextReqCounter())
	ch := make(chan []byte, 1)
	pendingReqs.Store(id, ch)
	defer pendingReqs.Delete(id)

	serverSend(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "elicitation/create",
		"params": map[string]interface{}{
			"message":         message,
			"requestedSchema": schema,
		},
	})

	select {
	case raw := <-ch:
		var resp struct {
			Result *elicitResult `json:"result"`
			Error  *mcpError     `json:"error"`
		}
		if jsonErr := json.Unmarshal(raw, &resp); jsonErr != nil {
			return nil, "", fmt.Errorf("failed to parse elicitation response: %w", jsonErr)
		}
		if resp.Error != nil {
			return nil, "", fmt.Errorf("elicitation not supported or failed: %s", resp.Error.Message)
		}
		if resp.Result == nil {
			return nil, "", fmt.Errorf("empty elicitation response")
		}
		return resp.Result.Content, resp.Result.Action, nil
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case <-time.After(elicitTimeout):
		return nil, "", fmt.Errorf("elicitation timed out")
	}
}

// ---- modern: Multi Round-Trip Requests --------------------------------------

// mrtrPrompt is one question a suspended handler is waiting on.
type mrtrPrompt struct {
	Key     string
	Message string
	Schema  map[string]interface{}
}

// mrtrOutcome is a finished handler's result.
type mrtrOutcome struct {
	Text  string
	IsErr bool
}

// mrtrExchange is one tools/call that has asked the client for input and is
// suspended until the client retries. It is addressed by a random ID that
// travels to the client as requestState and is replaced every round, so each
// requestState can be redeemed exactly once.
type mrtrExchange struct {
	tool   string
	cancel context.CancelFunc

	prompts chan mrtrPrompt
	answers chan elicitResult
	done    chan mrtrOutcome

	mu      sync.Mutex
	id      string
	seq     int
	current *mrtrPrompt // the question the client was last sent
}

// mrtrToolTimeout bounds a whole multi-round tool call, from the first
// request to the final answer.
const mrtrToolTimeout = 30 * time.Minute

// mrtrExchanges holds the suspended exchanges. An entry is taken out while a
// retry is being served (so a requestState cannot be redeemed twice
// concurrently) and put back only when the handler asks another question.
var mrtrExchanges sync.Map

var errMRTRUnknownState = errors.New("requestState is unknown or expired; call the tool again without requestState")

func newMRTRExchangeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b[:])
}

// startMRTRExchange runs tool in the background with this exchange as its
// elicitor. The handler's context is detached from the request that started it
// — that request ends as soon as the first question goes out — and bounded by
// toolTimeout instead.
func startMRTRExchange(ctx context.Context, tool string, args map[string]interface{}, toolTimeout time.Duration) *mrtrExchange {
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), toolTimeout)
	ex := &mrtrExchange{
		tool:    tool,
		cancel:  cancel,
		prompts: make(chan mrtrPrompt),
		answers: make(chan elicitResult),
		done:    make(chan mrtrOutcome, 1),
	}
	go func() {
		defer cancel()
		text, isErr := handleToolCall(withElicitor(hctx, ex), tool, args)
		ex.done <- mrtrOutcome{Text: text, IsErr: isErr}
	}()
	return ex
}

// resumeMRTRExchange takes the exchange named by state out of the store and
// hands the client's answer to the suspended handler. When the client did not
// answer the outstanding question, answered is false and the caller repeats
// the question.
func resumeMRTRExchange(tool, state string, responses map[string]json.RawMessage) (ex *mrtrExchange, answered bool, err error) {
	v, ok := mrtrExchanges.LoadAndDelete(state)
	if !ok {
		return nil, false, errMRTRUnknownState
	}
	ex = v.(*mrtrExchange)
	if ex.tool != tool {
		// Wrong tool for this handle: put it back untouched for its owner.
		mrtrExchanges.Store(state, ex)
		return nil, false, fmt.Errorf("requestState belongs to tool %q, not %q", ex.tool, tool)
	}
	ex.mu.Lock()
	cur := ex.current
	ex.mu.Unlock()
	if cur == nil {
		return ex, false, nil
	}
	raw, ok := responses[cur.Key]
	if !ok {
		return ex, false, nil
	}
	var res elicitResult
	if err := json.Unmarshal(raw, &res); err != nil {
		mrtrExchanges.Store(state, ex)
		return nil, false, fmt.Errorf("inputResponses[%q] is not an ElicitResult: %v", cur.Key, err)
	}
	select {
	case ex.answers <- res:
		ex.mu.Lock()
		ex.current = nil
		ex.mu.Unlock()
		return ex, true, nil
	case <-time.After(5 * time.Second):
		// The handler stopped waiting (timed out) between the lookup and now.
		return nil, false, errMRTRUnknownState
	}
}

// park stores the exchange under a fresh requestState and returns it. The
// previous handle (if any) was already removed by resumeMRTRExchange.
func (ex *mrtrExchange) park() string {
	id := newMRTRExchangeID()
	ex.mu.Lock()
	ex.id = id
	ex.mu.Unlock()
	mrtrExchanges.Store(id, ex)
	return id
}

// next waits until the handler either asks its next question or finishes, and
// returns the matching tools/call result. If the request serving this round
// is cancelled meanwhile, the whole exchange is cancelled with it.
func (ex *mrtrExchange) next(ctx context.Context) (interface{}, error) {
	select {
	case p := <-ex.prompts:
		ex.mu.Lock()
		ex.current = &p
		ex.mu.Unlock()
		return ex.inputRequiredResult(p, ex.park()), nil
	case out := <-ex.done:
		return newToolResult(out.Text, out.IsErr), nil
	case <-ctx.Done():
		ex.cancel()
		return nil, ctx.Err()
	}
}

// repeat re-sends the outstanding question to a client whose retry did not
// answer it, under a new requestState.
func (ex *mrtrExchange) repeat(ctx context.Context) (interface{}, error) {
	ex.mu.Lock()
	cur := ex.current
	ex.mu.Unlock()
	if cur == nil {
		// Between questions: wait for the next one (or the result).
		return ex.next(ctx)
	}
	return ex.inputRequiredResult(*cur, ex.park()), nil
}

func (ex *mrtrExchange) inputRequiredResult(p mrtrPrompt, state string) map[string]interface{} {
	return map[string]interface{}{
		"resultType": resultTypeInputRequired,
		"inputRequests": map[string]interface{}{
			p.Key: map[string]interface{}{
				"method": "elicitation/create",
				"params": map[string]interface{}{
					"mode":            "form",
					"message":         p.Message,
					"requestedSchema": p.Schema,
				},
			},
		},
		"requestState": state,
		"_meta":        serverInfoMeta(),
	}
}

// forget drops the exchange's current handle from the store.
func (ex *mrtrExchange) forget() {
	ex.mu.Lock()
	id := ex.id
	ex.mu.Unlock()
	if id != "" {
		mrtrExchanges.Delete(id)
	}
}

// elicit is called from the handler goroutine: it publishes the question and
// blocks until the client's retry delivers the answer.
func (ex *mrtrExchange) elicit(ctx context.Context, message string, schema map[string]interface{}) (map[string]interface{}, string, error) {
	ex.mu.Lock()
	ex.seq++
	key := fmt.Sprintf("elicit-%d", ex.seq)
	ex.mu.Unlock()

	select {
	case ex.prompts <- mrtrPrompt{Key: key, Message: message, Schema: schema}:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	select {
	case res := <-ex.answers:
		return res.Content, res.Action, nil
	case <-ctx.Done():
		ex.forget()
		return nil, "", ctx.Err()
	case <-time.After(elicitTimeout):
		// The client walked away: end the whole call, so the handler does
		// not carry on with defaults (login would open a browser).
		ex.forget()
		ex.cancel()
		return nil, "", fmt.Errorf("elicitation timed out")
	}
}
