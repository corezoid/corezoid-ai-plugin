package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type mcpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type mcpResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

const resourceURIPrefix = "corezoid://process/"

// listResources walks the local .processes directory and returns every
// .conv.json file as an MCP resource.
func listResources() ([]mcpResource, error) {
	var resources []mcpResource

	err := filepath.WalkDir(".processes", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".conv.json") {
			return nil
		}
		rel, err := filepath.Rel(".processes", path)
		if err != nil {
			return nil
		}
		uri := resourceURIPrefix + filepath.ToSlash(rel)
		resources = append(resources, mcpResource{
			URI:      uri,
			Name:     d.Name(),
			MimeType: "application/json",
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}
	return resources, nil
}

// readResource returns the content of the .conv.json file identified by uri.
// Only corezoid://process/ URIs are supported.
func readResource(uri string) (*mcpResourceContent, error) {
	if !strings.HasPrefix(uri, resourceURIPrefix) {
		return nil, fmt.Errorf("unsupported resource URI: %s", uri)
	}

	rel := uri[len(resourceURIPrefix):]
	clean := filepath.Clean(filepath.FromSlash(rel))
	if strings.HasPrefix(clean, "..") {
		return nil, fmt.Errorf("invalid resource path")
	}

	fullPath := filepath.Join(".processes", clean)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("resource not found: %w", err)
	}

	return &mcpResourceContent{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(data),
	}, nil
}
