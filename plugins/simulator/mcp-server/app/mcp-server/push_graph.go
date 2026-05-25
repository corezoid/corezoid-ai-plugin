package mcpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

)

// PushGraphResult holds the outcome of PushGraphFile.
type PushGraphResult struct {
	LayerID                                                                       string
	UpdatedGraph                                                                  GraphFile
	ActorsCreated, ActorsUpdated, ActorsUnchanged, ActorsDeleted, ActorsRecreated int
	EdgesCreated, EdgesDeleted                                                    int
}

// PushGraphFile syncs a parsed graph to the simulator API without touching the
// filesystem or environment variables.
//
//   - graph:         parsed graph to sync; graph.LayerID is used if layerID is empty
//   - workspaceID:   account/workspace ID used as accId in API URLs
//   - layerID:       target layer to sync (overrides graph.LayerID when graph.LayerID is empty)
//   - authorization: full Authorization header value, e.g. "Simulator <token>"
//   - baseURL:       simulator API base URL, e.g. "https://api.simulator.company/v/1.0"
func PushGraphFile(graph GraphFile, workspaceID, layerID, authorization, baseURL string) (PushGraphResult, error) {
	s := newGraphSyncer(baseURL, authorization, workspaceID)
	return s.pushGraph(context.Background(), graph, layerID)
}

// ---- Per-workspace cache ----

// workspaceCache holds API data that is stable within a workspace and can be
// reused across multiple PushGraphFile calls for the same workspaceID.
type workspaceCache struct {
	mu sync.RWMutex

	sysFormsLoaded bool
	sysFormsErr    error
	sysForms       []SysFormItem
	formNameToID   map[string]int
	formIDToName   map[int]string

	edgeTypeID int // "hierarchy" edge type, 0 = not yet fetched

	formFields   map[int]map[string]interface{} // formId → field map
	actorFormIDs map[string]int                 // actor UUID → formId
}

func newWorkspaceCache() *workspaceCache {
	return &workspaceCache{
		formNameToID: map[string]int{},
		formIDToName: map[int]string{},
		formFields:   map[int]map[string]interface{}{},
		actorFormIDs: map[string]int{},
	}
}

var (
	wsCachesMu sync.RWMutex
	wsCaches   = map[string]*workspaceCache{}
)

func getWorkspaceCache(workspaceID string) *workspaceCache {
	wsCachesMu.RLock()
	c := wsCaches[workspaceID]
	wsCachesMu.RUnlock()
	if c != nil {
		return c
	}
	wsCachesMu.Lock()
	defer wsCachesMu.Unlock()
	if c = wsCaches[workspaceID]; c != nil { // double-check under write lock
		return c
	}
	c = newWorkspaceCache()
	wsCaches[workspaceID] = c
	return c
}

// ---- GraphSyncer ----

// GraphSyncer carries the HTTP config for one push operation.
// Expensive API data (forms, edge types) is cached in workspaceCache and
// reused across calls for the same workspaceID.
type GraphSyncer struct {
	baseURL     string
	auth        string // full Authorization header value, e.g. "Simulator <token>"
	workspaceID string
	httpClient  *http.Client
	cache       *workspaceCache
}

func newGraphSyncer(baseURL, authorization, workspaceID string) *GraphSyncer {
	return &GraphSyncer{
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		auth:        authorization,
		workspaceID: workspaceID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
		cache: getWorkspaceCache(workspaceID),
	}
}

// ---- HTTP primitives ----

func (s *GraphSyncer) get(ctx context.Context, apiURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", s.auth)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: HTTP %d: %.300s", apiURL, resp.StatusCode, data)
	}
	return data, nil
}

func (s *GraphSyncer) doJSON(ctx context.Context, method, apiURL string, body interface{}) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", s.auth)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %.300s", method, apiURL, resp.StatusCode, data)
	}
	return data, nil
}

func (s *GraphSyncer) post(ctx context.Context, apiURL string, body interface{}) ([]byte, error) {
	return s.doJSON(ctx, "POST", apiURL, body)
}

func (s *GraphSyncer) put(ctx context.Context, apiURL string, body interface{}) ([]byte, error) {
	return s.doJSON(ctx, "PUT", apiURL, body)
}

// ---- Layer data ----

func (s *GraphSyncer) fetchLayerActors(ctx context.Context, layerID string) ([]layerActor, error) {
	var all []layerActor
	limit, offset := 50, 0
	for {
		u := fmt.Sprintf("%s/graph_layers/paginated/%s?type=nodes&limit=%d&offset=%d",
			s.baseURL, layerID, limit, offset)
		body, err := s.get(ctx, u)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []layerActor `json:"data"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse layer actors: %w (body: %.200s)", err, body)
		}
		all = append(all, page.Data...)
		if len(page.Data) < limit {
			break
		}
		offset += limit
	}
	return all, nil
}

func (s *GraphSyncer) fetchLayerEdges(ctx context.Context, layerID string) ([]layerEdge, error) {
	var all []layerEdge
	limit, offset := 50, 0
	for {
		u := fmt.Sprintf("%s/graph_layers/paginated/%s?type=edges&limit=%d&offset=%d",
			s.baseURL, layerID, limit, offset)
		body, err := s.get(ctx, u)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []layerEdge `json:"data"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse layer edges: %w (body: %.200s)", err, body)
		}
		all = append(all, page.Data...)
		if len(page.Data) < limit {
			break
		}
		offset += limit
	}
	return all, nil
}

// ---- Per-instance cache helpers ----

// cacheActorFormIDFromResult parses a createActor/getActor response and stores
// the UUID→formId mapping in the workspace cache.
func (s *GraphSyncer) cacheActorFormIDFromResult(responseText string) {
	var resp struct {
		Data struct {
			ID     string                 `json:"id"`
			FormID int                    `json:"formId"`
			Data   map[string]interface{} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &resp); err != nil || resp.Data.ID == "" {
		return
	}
	formID := 0
	for key := range resp.Data.Data {
		if !strings.HasPrefix(key, "__form__") {
			continue
		}
		rest := strings.TrimPrefix(key, "__form__")
		if idx := strings.Index(rest, ":"); idx > 0 {
			if fid := toInt(rest[:idx]); fid != 0 {
				formID = fid
				break
			}
		}
	}
	if formID == 0 {
		formID = resp.Data.FormID
	}
	if formID == 0 {
		return
	}
	s.cache.mu.Lock()
	s.cache.actorFormIDs[resp.Data.ID] = formID
	s.cache.mu.Unlock()
}

// overrideActorFormID corrects the cached formId for an actor to the original
// child formId (needed when the actor was stored under its parent formId).
func (s *GraphSyncer) overrideActorFormID(responseText string, childFormID int) {
	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseText), &resp); err != nil || resp.Data.ID == "" {
		return
	}
	s.cache.mu.Lock()
	s.cache.actorFormIDs[resp.Data.ID] = childFormID
	s.cache.mu.Unlock()
}

func (s *GraphSyncer) buildFormNameIDCache(forms []SysFormItem) {
	// called while holding s.cache.mu (write) — no extra locking needed
	for k := range s.cache.formNameToID {
		delete(s.cache.formNameToID, k)
	}
	for k := range s.cache.formIDToName {
		delete(s.cache.formIDToName, k)
	}
	var walk func([]SysFormItem)
	walk = func(items []SysFormItem) {
		for _, item := range items {
			if item.Title != "" && item.ID != 0 {
				s.cache.formNameToID[item.Title] = item.ID
				s.cache.formIDToName[item.ID] = item.Title
			}
			walk(item.Childs)
		}
	}
	walk(forms)
}

func (s *GraphSyncer) resolveFormNameToID(name string) int {
	s.cache.mu.RLock()
	id := s.cache.formNameToID[name]
	s.cache.mu.RUnlock()
	return id
}

// ---- Form / edge-type data ----

func (s *GraphSyncer) fetchFormFieldValues(ctx context.Context, formID int) (map[string]interface{}, error) {
	s.cache.mu.RLock()
	cached := s.cache.formFields[formID]
	s.cache.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	u := fmt.Sprintf("%s/forms/%d", s.baseURL, formID)
	data, err := s.get(ctx, u)
	if err != nil {
		return nil, err
	}

	var apiResult struct {
		Data struct {
			Form struct {
				PictureBase64 string `json:"pictureBase64"`
				Sections      []struct {
					Content []struct {
						ID    string      `json:"id"`
						Value interface{} `json:"value"`
					} `json:"content"`
				} `json:"sections"`
			} `json:"form"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &apiResult); err != nil {
		return nil, fmt.Errorf("failed to parse /forms/%d response: %w", formID, err)
	}

	fields := map[string]interface{}{}
	for _, sec := range apiResult.Data.Form.Sections {
		for _, f := range sec.Content {
			value := f.Value
			if f.ID == "view" {
				if str, ok := value.(string); ok && str != "" {
					var parsed interface{}
					if jsonErr := json.Unmarshal([]byte(str), &parsed); jsonErr == nil {
						value = parsed
					}
				}
			}
			fields[f.ID] = value
		}
	}
	if apiResult.Data.Form.PictureBase64 != "" {
		fields["pictureBase64"] = apiResult.Data.Form.PictureBase64
	}

	s.cache.mu.Lock()
	s.cache.formFields[formID] = fields
	s.cache.mu.Unlock()
	return fields, nil
}

func (s *GraphSyncer) fetchHierarchyEdgeTypeID(ctx context.Context) (int, error) {
	s.cache.mu.RLock()
	cached := s.cache.edgeTypeID
	s.cache.mu.RUnlock()
	if cached != 0 {
		return cached, nil
	}

	u := fmt.Sprintf("%s/edge_types/%s", s.baseURL, s.workspaceID)
	data, err := s.get(ctx, u)
	if err != nil {
		return 0, err
	}
	var result struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, fmt.Errorf("failed to parse getEdgeTypes response: %w", err)
	}
	for _, et := range result.Data {
		if et.Name == "hierarchy" {
			s.cache.mu.Lock()
			s.cache.edgeTypeID = et.ID
			s.cache.mu.Unlock()
			return et.ID, nil
		}
	}
	return 0, fmt.Errorf("hierarchy edge type not found")
}

func (s *GraphSyncer) resolveActorFormID(ctx context.Context, actorID string) int {
	s.cache.mu.RLock()
	fid := s.cache.actorFormIDs[actorID]
	s.cache.mu.RUnlock()
	if fid != 0 {
		return fid
	}

	u := fmt.Sprintf("%s/actors/%s", s.baseURL, actorID)
	data, err := s.get(ctx, u)
	if err != nil {
		log.Printf("resolveActorFormID: getActor failed for %s: %v", actorID, err)
		return 0
	}
	s.cacheActorFormIDFromResult(string(data))

	s.cache.mu.RLock()
	fid = s.cache.actorFormIDs[actorID]
	s.cache.mu.RUnlock()
	return fid
}

// loadSysForms returns system forms for this workspace, fetching from the API
// on first call and reusing the cached result on subsequent calls.
func (s *GraphSyncer) loadSysForms(ctx context.Context) ([]SysFormItem, error) {
	s.cache.mu.RLock()
	loaded := s.cache.sysFormsLoaded
	forms, cacheErr := s.cache.sysForms, s.cache.sysFormsErr
	s.cache.mu.RUnlock()
	if loaded {
		return forms, cacheErr
	}

	u := fmt.Sprintf("%s/forms/templates/system/%s?formTypes=system", s.baseURL, s.workspaceID)
	data, err := s.get(ctx, u)
	if err != nil {
		s.cache.mu.Lock()
		s.cache.sysFormsErr = fmt.Errorf("getSystemForms: %w", err)
		s.cache.sysFormsLoaded = true
		s.cache.mu.Unlock()
		return nil, s.cache.sysFormsErr
	}

	var apiResult struct {
		Data []struct {
			ID          int    `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			ParentID    *int   `json:"parentId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &apiResult); err != nil {
		s.cache.mu.Lock()
		s.cache.sysFormsErr = fmt.Errorf("parse system forms: %w", err)
		s.cache.sysFormsLoaded = true
		s.cache.mu.Unlock()
		return nil, s.cache.sysFormsErr
	}

	allowedRootTitles := map[string]bool{
		"Graphs": true, "Layers": true, "FlowchartBlock": true, "Actor": true, "Null": true,
	}
	childrenOf := map[int][]SysFormItem{}
	var roots []SysFormItem
	for _, item := range apiResult.Data {
		form := SysFormItem{ID: item.ID, Title: item.Title, Description: item.Description}
		if item.ParentID == nil {
			if allowedRootTitles[item.Title] {
				roots = append(roots, form)
			}
		} else {
			childrenOf[*item.ParentID] = append(childrenOf[*item.ParentID], form)
		}
	}
	for i := range roots {
		if ch, ok := childrenOf[roots[i].ID]; ok {
			roots[i].Childs = ch
		}
	}

	s.cache.mu.Lock()
	s.cache.sysForms = roots
	s.cache.sysFormsLoaded = true
	s.buildFormNameIDCache(roots) // runs inside the write lock
	s.cache.mu.Unlock()
	return roots, nil
}

// ---- Inject helpers ----

func (s *GraphSyncer) injectCreateActorData(ctx context.Context, args, queryParams map[string]interface{}) (int, error) {
	formID := toInt(queryParams["formId"])
	if formID == 0 {
		return 0, nil
	}

	fields, err := s.fetchFormFieldValues(ctx, formID)
	if err != nil {
		return 0, err
	}

	sysForms, sysErr := s.loadSysForms(ctx)
	if sysErr != nil || sysForms == nil {
		return 0, nil
	}

	parentID, isChild, found := findFormInTree(sysForms, formID, 0)
	if !found || !isChild {
		return 0, nil
	}

	autoData := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		autoData[fmt.Sprintf("__form__%d:%s", formID, k)] = v
	}

	body := map[string]interface{}{}
	if bodyStr, ok := args["body"].(string); ok && bodyStr != "" {
		_ = json.Unmarshal([]byte(bodyStr), &body)
	}
	body["data"] = autoData

	newBody, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	args["body"] = string(newBody)
	queryParams["formId"] = strconv.Itoa(parentID)
	return formID, nil
}

func (s *GraphSyncer) injectManageLayerData(ctx context.Context, args map[string]interface{}) error {
	bodyStr, ok := args["body"].(string)
	if !ok || bodyStr == "" {
		return nil
	}
	var body []interface{}
	if err := json.Unmarshal([]byte(bodyStr), &body); err != nil {
		return nil
	}
	if len(body) == 0 {
		return nil
	}

	modified := false
	for _, raw := range body {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		data, ok := item["data"].(map[string]interface{})
		if !ok {
			continue
		}
		formID := toInt(data["id"])
		if formID == 0 {
			if actorID, ok := data["id"].(string); ok && actorID != "" {
				formID = s.resolveActorFormID(ctx, actorID)
			}
		}
		if formID == 0 {
			continue
		}
		fields, err := s.fetchFormFieldValues(ctx, formID)
		if err != nil {
			log.Printf("Warning: manageLayer failed to fetch form %d: %v", formID, err)
			continue
		}
		pictureBase64, ok := fields["pictureBase64"].(string)
		if !ok || pictureBase64 == "" {
			continue
		}
		areaPicture, _ := data["areaPicture"].(map[string]interface{})
		if areaPicture == nil {
			areaPicture = map[string]interface{}{}
		}
		areaPicture["img"] = pictureBase64
		areaPicture["type"] = "flowchart"

		var viewMap map[string]interface{}
		if viewRaw := fields["view"]; viewRaw != nil {
			viewMap, _ = viewRaw.(map[string]interface{})
		}
		if _, hasH := areaPicture["height"]; !hasH {
			if viewMap != nil {
				if sizeRaw, ok := viewMap["size"].(map[string]interface{}); ok {
					if h, ok := sizeRaw["h"]; ok {
						areaPicture["height"] = h
					}
					if w, ok := sizeRaw["w"]; ok {
						areaPicture["width"] = w
					}
				}
			}
		}
		data["areaPicture"] = areaPicture

		layerSettings := map[string]interface{}{
			"height": areaPicture["height"],
			"width":  areaPicture["width"],
		}
		if blockID, ok := fields["blockId"].(string); ok && blockID != "" {
			layerSettings["blockId"] = blockID
		}
		if viewMap != nil {
			if shape, ok := viewMap["shape"]; ok {
				layerSettings["shape"] = shape
			}
			if textFrame, ok := viewMap["textFrame"]; ok {
				layerSettings["textFrame"] = textFrame
			}
		}
		data["layerSettings"] = layerSettings
		modified = true
	}

	if modified {
		newBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		args["body"] = string(newBody)
	}
	return nil
}

func (s *GraphSyncer) injectMassLinkData(ctx context.Context, args map[string]interface{}) error {
	typeID, err := s.fetchHierarchyEdgeTypeID(ctx)
	if err != nil {
		return err
	}

	bodyStr, _ := args["body"].(string)
	var items []map[string]interface{}
	if bodyStr != "" {
		if err := json.Unmarshal([]byte(bodyStr), &items); err != nil {
			return fmt.Errorf("massLink body is not a JSON array: %w", err)
		}
	}

	changed := false
	for i, item := range items {
		if _, hasTypeID := item["edgeTypeId"]; !hasTypeID {
			items[i]["edgeTypeId"] = typeID
			changed = true
		}
	}

	if changed || bodyStr == "" {
		newBody, err := json.Marshal(items)
		if err != nil {
			return err
		}
		args["body"] = string(newBody)
	}
	return nil
}

// ---- API mutations ----

func (s *GraphSyncer) createGraphActor(ctx context.Context, a GraphActor) (string, error) {
	formID := a.FormID
	if a.FormName != "" {
		if _, loadErr := s.loadSysForms(ctx); loadErr != nil {
			return "", fmt.Errorf("load sys forms: %w", loadErr)
		}
		if id := s.resolveFormNameToID(a.FormName); id != 0 {
			formID = id
		} else {
			return "", fmt.Errorf("form name %q not found", a.FormName)
		}
	}
	if formID == 0 {
		return "", fmt.Errorf("actor %q: formId or formName required", a.Title)
	}

	body := map[string]interface{}{
		"title":       a.Title,
		"description": a.Description,
		"color":       a.Color,
		"picture":     a.Picture,
	}
	if a.Data != nil {
		body["data"] = a.Data
	}
	omitEmptyFields(body)

	bodyBytes, _ := json.Marshal(body)
	actorArgs := map[string]interface{}{"body": string(bodyBytes)}
	qp := map[string]interface{}{"formId": float64(formID)}

	childFormID, injErr := s.injectCreateActorData(ctx, actorArgs, qp)
	if injErr != nil {
		log.Printf("Warning: syncGraph createActor data injection: %v", injErr)
	}

	finalFormID := toInt(qp["formId"])
	if finalFormID == 0 {
		finalFormID = formID
	}
	var bodyToSend interface{}
	if bodyStr, ok := actorArgs["body"].(string); ok {
		var m map[string]interface{}
		_ = json.Unmarshal([]byte(bodyStr), &m)
		bodyToSend = m
	}

	u := fmt.Sprintf("%s/actors/actor/%d", s.baseURL, finalFormID)
	respBytes, err := s.post(ctx, u, bodyToSend)
	if err != nil {
		return "", err
	}

	responseText := string(respBytes)
	s.cacheActorFormIDFromResult(responseText)
	if childFormID != 0 {
		s.overrideActorFormID(responseText, childFormID)
	}

	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal(respBytes, &resp); jsonErr == nil && resp.Data.ID != "" {
		return resp.Data.ID, nil
	}
	return "", fmt.Errorf("createActor: no ID in response (%.200s)", responseText)
}

func (s *GraphSyncer) updateGraphActor(ctx context.Context, sa layerActor, fa GraphActor) (bool, error) {
	if sa.Title == fa.Title && sa.Description == fa.Description && sa.Color == fa.Color && sa.Picture == fa.Picture {
		return false, nil
	}

	body := map[string]interface{}{
		"title":       fa.Title,
		"description": fa.Description,
		"color":       fa.Color,
		"picture":     fa.Picture,
	}
	if fa.Data != nil {
		body["data"] = fa.Data
	}

	childFormID := formIDFromLayerActor(sa)
	apiFormID := childFormID
	if sysForms, sysErr := s.loadSysForms(ctx); sysErr == nil && sysForms != nil {
		if parentID, isChild, found := findFormInTree(sysForms, childFormID, 0); found && isChild {
			apiFormID = parentID
		}
	}

	u := fmt.Sprintf("%s/actors/actor/%d/%s?replaceEmpty=false", s.baseURL, apiFormID, sa.ID)
	if _, err := s.put(ctx, u, body); err != nil {
		return false, err
	}
	return true, nil
}

func (s *GraphSyncer) callManageLayer(ctx context.Context, layerID string, items []manageLayerItem) error {
	const batchSize = 50
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[i:end]

		batchBytes, _ := json.Marshal(batch)
		innerArgs := map[string]interface{}{"body": string(batchBytes)}
		if injErr := s.injectManageLayerData(ctx, innerArgs); injErr != nil {
			log.Printf("Warning: callManageLayer injectManageLayerData: %v", injErr)
		}

		var bodyToSend interface{}
		if bodyStr, ok := innerArgs["body"].(string); ok {
			var arr []interface{}
			_ = json.Unmarshal([]byte(bodyStr), &arr)
			bodyToSend = arr
		}

		u := fmt.Sprintf("%s/graph_layers/actors/%s", s.baseURL, layerID)
		if _, err := s.post(ctx, u, bodyToSend); err != nil {
			return fmt.Errorf("manageLayer batch %d: %w", i/batchSize, err)
		}
	}
	return nil
}

func (s *GraphSyncer) updatePositions(ctx context.Context, layerID string, updates []map[string]interface{}) error {
	u := fmt.Sprintf("%s/graph_layers/actors/%s", s.baseURL, layerID)
	_, err := s.put(ctx, u, updates)
	return err
}

func (s *GraphSyncer) createEdgeLink(ctx context.Context, srcUUID, tgtUUID string) (string, error) {
	links := []map[string]interface{}{
		{"source": srcUUID, "target": tgtUUID},
	}
	linksBytes, _ := json.Marshal(links)
	innerArgs := map[string]interface{}{"body": string(linksBytes)}
	if injErr := s.injectMassLinkData(ctx, innerArgs); injErr != nil {
		return "", fmt.Errorf("inject massLink data: %w", injErr)
	}

	var bodyToSend interface{}
	if bodyStr, ok := innerArgs["body"].(string); ok {
		var arr []interface{}
		_ = json.Unmarshal([]byte(bodyStr), &arr)
		bodyToSend = arr
	}

	u := fmt.Sprintf("%s/actors/mass_links/%s", s.baseURL, s.workspaceID)
	respBytes, err := s.post(ctx, u, bodyToSend)
	if err != nil {
		return "", err
	}

	var resp struct {
		Data []struct {
			Error bool `json:"error"`
			Data  struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal(respBytes, &resp); jsonErr == nil && len(resp.Data) > 0 && resp.Data[0].Data.ID != "" {
		return resp.Data[0].Data.ID, nil
	}
	return "", fmt.Errorf("massLink: no link ID in response")
}

// ---- Main sync logic ----

func (s *GraphSyncer) pushGraph(ctx context.Context, graph GraphFile, layerID string) (PushGraphResult, error) {
	var result PushGraphResult

	if graph.LayerID == "" {
		graph.LayerID = layerID
	}
	result.LayerID = graph.LayerID

	// Partial update mode: triggered when any actor or edge has an explicit action field.
	// In this mode server elements not listed in the file are left untouched.
	partialMode := false
	for _, a := range graph.Actors {
		if a.Action != "" {
			partialMode = true
			break
		}
	}
	if !partialMode {
		for _, e := range graph.Edges {
			if e.Action != "" {
				partialMode = true
				break
			}
		}
	}

	serverActors, err := s.fetchLayerActors(ctx, graph.LayerID)
	if err != nil {
		return result, fmt.Errorf("fetch layer actors: %w", err)
	}
	serverEdges, err := s.fetchLayerEdges(ctx, graph.LayerID)
	if err != nil {
		return result, fmt.Errorf("fetch layer edges: %w", err)
	}

	serverActorByUUID := make(map[string]layerActor, len(serverActors))
	for _, a := range serverActors {
		serverActorByUUID[a.ID] = a
	}
	type edgePair struct{ src, tgt string }
	serverEdgeByPair := make(map[edgePair]layerEdge, len(serverEdges))
	for _, e := range serverEdges {
		serverEdgeByPair[edgePair{e.Source, e.Target}] = e
	}

	idMap := make(map[string]string, len(graph.Actors))
	fileUUIDs := make(map[string]bool, len(graph.Actors))
	replacedUUIDs := make(map[string]bool)

	var nodeManageItems []manageLayerItem
	var posUpdates []map[string]interface{}

	for i := range graph.Actors {
		a := &graph.Actors[i]
		origID := a.ID

		if partialMode && a.Action == "del" {
			if isUUID(origID) {
				if sa, onLayer := serverActorByUUID[origID]; onLayer {
					var item manageLayerItem
					item.Action = "delete"
					item.Data.ID = origID
					item.Data.Type = "node"
					item.Data.LaID = sa.LaID
					nodeManageItems = append(nodeManageItems, item)
					result.ActorsDeleted++
				}
			}
			continue
		}

		if isUUID(origID) {
			idMap[origID] = origID
			fileUUIDs[origID] = true

			sa, onLayer := serverActorByUUID[origID]
			if onLayer {
				fileFormID := a.FormID
				if a.FormName != "" {
					if _, loadErr := s.loadSysForms(ctx); loadErr == nil {
						if id := s.resolveFormNameToID(a.FormName); id != 0 {
							fileFormID = id
						}
					}
				}
				serverFormID := formIDFromLayerActor(sa)

				if fileFormID != 0 && serverFormID != 0 && fileFormID != serverFormID {
					var delItem manageLayerItem
					delItem.Action = "delete"
					delItem.Data.ID = origID
					delItem.Data.Type = "node"
					delItem.Data.LaID = sa.LaID
					nodeManageItems = append(nodeManageItems, delItem)
					replacedUUIDs[origID] = true

					serverUUID, createErr := s.createGraphActor(ctx, *a)
					if createErr != nil {
						return result, fmt.Errorf("recreate actor %q (formId %d→%d): %w",
							a.Title, serverFormID, fileFormID, createErr)
					}
					idMap[origID] = serverUUID
					a.ID = serverUUID
					fileUUIDs[serverUUID] = true

					var addItem manageLayerItem
					addItem.Action = "create"
					addItem.Data.ID = serverUUID
					addItem.Data.Type = "node"
					addItem.Data.Position.X = a.Position.X
					addItem.Data.Position.Y = a.Position.Y
					nodeManageItems = append(nodeManageItems, addItem)
					result.ActorsRecreated++
				} else {
					changed, updateErr := s.updateGraphActor(ctx, sa, *a)
					if updateErr != nil {
						log.Printf("Warning: update actor %s: %v", origID, updateErr)
					}
					if changed {
						result.ActorsUpdated++
					} else {
						result.ActorsUnchanged++
					}
					if sa.Position.X != a.Position.X || sa.Position.Y != a.Position.Y {
						posUpdates = append(posUpdates, map[string]interface{}{
							"id":       sa.LaID,
							"position": map[string]int{"x": a.Position.X, "y": a.Position.Y},
						})
					}
				}
			} else {
				var item manageLayerItem
				item.Action = "create"
				item.Data.ID = origID
				item.Data.Type = "node"
				item.Data.Position.X = a.Position.X
				item.Data.Position.Y = a.Position.Y
				nodeManageItems = append(nodeManageItems, item)
				result.ActorsCreated++
			}
		} else {
			serverUUID, createErr := s.createGraphActor(ctx, *a)
			if createErr != nil {
				return result, fmt.Errorf("create actor %q: %w", a.Title, createErr)
			}
			idMap[origID] = serverUUID
			a.ID = serverUUID
			fileUUIDs[serverUUID] = true

			var item manageLayerItem
			item.Action = "create"
			item.Data.ID = serverUUID
			item.Data.Type = "node"
			item.Data.Position.X = a.Position.X
			item.Data.Position.Y = a.Position.Y
			nodeManageItems = append(nodeManageItems, item)
			result.ActorsCreated++
		}
	}

	// In full sync mode delete server actors not present in the file.
	if !partialMode {
		for _, sa := range serverActors {
			if !fileUUIDs[sa.ID] && !replacedUUIDs[sa.ID] {
				var item manageLayerItem
				item.Action = "delete"
				item.Data.ID = sa.ID
				item.Data.Type = "node"
				item.Data.LaID = sa.LaID
				nodeManageItems = append(nodeManageItems, item)
				result.ActorsDeleted++
			}
		}
	}

	if len(nodeManageItems) > 0 {
		if err := s.callManageLayer(ctx, graph.LayerID, nodeManageItems); err != nil {
			return result, fmt.Errorf("manageLayer nodes: %w", err)
		}
	}

	if len(posUpdates) > 0 {
		if posErr := s.updatePositions(ctx, graph.LayerID, posUpdates); posErr != nil {
			log.Printf("Warning: update positions: %v", posErr)
		}
	}

	// Remap edge references to resolved server UUIDs, then marshal updated YAML.
	for i := range graph.Edges {
		if uuid, ok := idMap[graph.Edges[i].Source]; ok {
			graph.Edges[i].Source = uuid
		}
		if uuid, ok := idMap[graph.Edges[i].Target]; ok {
			graph.Edges[i].Target = uuid
		}
	}
	result.UpdatedGraph = graph

	// Re-fetch actors to obtain laId for newly added nodes (required for edge placement).
	updatedActors, err := s.fetchLayerActors(ctx, graph.LayerID)
	if err != nil {
		return result, fmt.Errorf("re-fetch layer actors: %w", err)
	}
	laIDByUUID := make(map[string]int, len(updatedActors))
	for _, a := range updatedActors {
		laIDByUUID[a.ID] = a.LaID
	}

	var edgeManageItems []manageLayerItem
	fileEdgePairs := make(map[edgePair]bool, len(graph.Edges))

	for _, e := range graph.Edges {
		srcUUID := idMap[e.Source]
		if srcUUID == "" {
			srcUUID = e.Source
		}
		tgtUUID := idMap[e.Target]
		if tgtUUID == "" {
			tgtUUID = e.Target
		}
		pair := edgePair{srcUUID, tgtUUID}

		if partialMode && e.Action == "del" {
			if se, exists := serverEdgeByPair[pair]; exists {
				var item manageLayerItem
				item.Action = "delete"
				item.Data.ID = se.ID
				item.Data.Type = "edge"
				item.Data.LaID = se.LaID
				edgeManageItems = append(edgeManageItems, item)
				result.EdgesDeleted++
			}
			continue
		}

		fileEdgePairs[pair] = true

		if _, exists := serverEdgeByPair[pair]; !exists {
			linkID, linkErr := s.createEdgeLink(ctx, srcUUID, tgtUUID)
			if linkErr != nil {
				return result, fmt.Errorf("create link %s→%s: %w", srcUUID, tgtUUID, linkErr)
			}
			srcLaID := laIDByUUID[srcUUID]
			tgtLaID := laIDByUUID[tgtUUID]
			if srcLaID != 0 && tgtLaID != 0 {
				var item manageLayerItem
				item.Action = "create"
				item.Data.ID = linkID
				item.Data.Type = "edge"
				item.Data.LaIDSrc = srcLaID
				item.Data.LaIDTgt = tgtLaID
				edgeManageItems = append(edgeManageItems, item)
			}
			result.EdgesCreated++
		}
	}

	// In full sync mode delete server edges not present in the file.
	if !partialMode {
		for _, se := range serverEdges {
			pair := edgePair{se.Source, se.Target}
			if !fileEdgePairs[pair] {
				var item manageLayerItem
				item.Action = "delete"
				item.Data.ID = se.ID
				item.Data.Type = "edge"
				item.Data.LaID = se.LaID
				edgeManageItems = append(edgeManageItems, item)
				result.EdgesDeleted++
			}
		}
	}

	if len(edgeManageItems) > 0 {
		if err := s.callManageLayer(ctx, graph.LayerID, edgeManageItems); err != nil {
			return result, fmt.Errorf("manageLayer edges: %w", err)
		}
	}

	return result, nil
}
