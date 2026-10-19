package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// subscriptions/listen (MCP 2026-07-28) replaces resources/subscribe and the
// standalone GET SSE stream. A client opens one long-lived request naming the
// notifications it wants; the server acknowledges the subset it honours and
// then streams those notifications, each tagged with the subscription's ID
// (the listen request's JSON-RPC id) in _meta. Cancelling the request ends
// the subscription: notifications/cancelled on stdio, closing the response
// stream on HTTP. The server never ends a subscription on its own, so it
// never sends the closing result.
//
// The tool and prompt lists are compiled into the binary and never change
// while the server runs, so toolsListChanged / promptsListChanged are honoured
// but never fire. Resources are the local .processes/*.conv.json files; they
// are polled, since a portable file watcher would be a new dependency.

// resourcePollInterval is how often a resource subscription rescans
// .processes. A var so tests can shorten it.
var resourcePollInterval = 2 * time.Second

// subscriptionFilter is subscriptions/listen's params.notifications.
type subscriptionFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged,omitempty"`
	PromptsListChanged    bool     `json:"promptsListChanged,omitempty"`
	ResourcesListChanged  bool     `json:"resourcesListChanged,omitempty"`
	ResourceSubscriptions []string `json:"resourceSubscriptions,omitempty"`
}

// parseSubscriptionFilter reads params.notifications and drops resource URIs
// this server cannot serve, so the acknowledgement names only what will
// actually be delivered.
func parseSubscriptionFilter(params json.RawMessage) (subscriptionFilter, *mcpError) {
	var p struct {
		Notifications *subscriptionFilter `json:"notifications"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil || p.Notifications == nil {
		return subscriptionFilter{}, &mcpError{Code: -32602, Message: "invalid params: subscriptions/listen requires params.notifications"}
	}
	f := *p.Notifications
	var uris []string
	for _, uri := range f.ResourceSubscriptions {
		if _, ok := resourceRelPath(uri); ok {
			uris = append(uris, uri)
		}
	}
	f.ResourceSubscriptions = uris
	return f, nil
}

// resourceRelPath maps a corezoid://process/ URI to its path under .processes.
func resourceRelPath(uri string) (string, bool) {
	if !strings.HasPrefix(uri, resourceURIPrefix) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(uri[len(resourceURIPrefix):]))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", false
	}
	return clean, true
}

// subscriptionNotification builds a notification tagged with the subscription.
func subscriptionNotification(subID interface{}, method string, params map[string]interface{}) map[string]interface{} {
	if params == nil {
		params = map[string]interface{}{}
	}
	params["_meta"] = map[string]interface{}{metaKeySubscriptionID: subID}
	return map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params}
}

// runSubscription acknowledges filter and streams matching notifications
// through emit until ctx is done or emit fails. It returns the error that
// ended it (ctx.Err() on cancellation).
func runSubscription(ctx context.Context, subID interface{}, filter subscriptionFilter, emit func(msg interface{}) error) error {
	ack := subscriptionNotification(subID, "notifications/subscriptions/acknowledged", map[string]interface{}{
		"notifications": filter,
	})
	if err := emit(ack); err != nil {
		return err
	}

	if !filter.ResourcesListChanged && len(filter.ResourceSubscriptions) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}

	prev := snapshotResources()
	ticker := time.NewTicker(resourcePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		cur := snapshotResources()
		if filter.ResourcesListChanged && !sameResourceSet(prev, cur) {
			if err := emit(subscriptionNotification(subID, "notifications/resources/list_changed", nil)); err != nil {
				return err
			}
		}
		for _, uri := range filter.ResourceSubscriptions {
			if prev[uri] != cur[uri] {
				if err := emit(subscriptionNotification(subID, "notifications/resources/updated", map[string]interface{}{"uri": uri})); err != nil {
					return err
				}
			}
		}
		prev = cur
	}
}

// snapshotResources maps each resource URI to a change stamp (mtime + size).
func snapshotResources() map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(".processes", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".conv.json") {
			return nil
		}
		rel, err := filepath.Rel(".processes", path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out[resourceURIPrefix+filepath.ToSlash(rel)] = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
		return nil
	})
	return out
}

func sameResourceSet(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
