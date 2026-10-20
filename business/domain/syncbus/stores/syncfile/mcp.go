package syncfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/loadkpi/baton/business/domain/syncbus"
	"github.com/loadkpi/baton/business/types/tag"
)

// fileMCP is the JSON document of the source's mcp.json. Primitive types only.
type fileMCP struct {
	MCPServers map[string]map[string]any `json:"mcpServers"`
}

// readMCP reads <source>/mcp.json; a source without one has no MCP servers.
func readMCP(source string) ([]syncbus.MCPServer, error) {
	data, err := os.ReadFile(filepath.Join(source, "mcp.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var doc fileMCP
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields() // a misspelled "mcpServers" must not silently sync nothing
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("mcp.json: %w", err)
	}

	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]syncbus.MCPServer, 0, len(names))
	for _, name := range names {
		server, err := toBusMCPServer(name, doc.MCPServers[name])
		if err != nil {
			return nil, fmt.Errorf("mcp.json: server %q: %w", name, err)
		}
		servers = append(servers, server)
	}
	return servers, nil
}

// toBusMCPServer takes the "tags" member out of a server's document: it is
// Baton's, not part of the configuration Claude reads.
func toBusMCPServer(name string, doc map[string]any) (syncbus.MCPServer, error) {
	spec := make(map[string]any, len(doc))
	for k, v := range doc {
		spec[k] = v
	}
	server := syncbus.MCPServer{Name: name, Spec: spec}

	raw, ok := spec["tags"]
	if !ok {
		return server, nil
	}
	delete(spec, "tags")

	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return syncbus.MCPServer{}, fmt.Errorf("tags must be a non-empty list of strings")
	}
	list := make([]string, 0, len(items))
	for _, item := range items {
		str, ok := item.(string)
		if !ok {
			return syncbus.MCPServer{}, fmt.Errorf("tags must be a non-empty list of strings")
		}
		list = append(list, str)
	}
	var err error
	if server.Tags, err = tag.ParseAll(list); err != nil {
		return syncbus.MCPServer{}, fmt.Errorf("tags: %w", err)
	}
	return server, nil
}

// QuerySecrets reads the secrets file: NAME=value lines.
func (s *Store) QuerySecrets(_ context.Context) (syncbus.Secrets, error) {
	secrets := syncbus.Secrets{Path: s.secretsPath, Values: map[string]string{}}
	info, err := os.Stat(s.secretsPath)
	if os.IsNotExist(err) {
		return secrets, nil
	}
	if err != nil {
		return syncbus.Secrets{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return syncbus.Secrets{}, fmt.Errorf("%s is accessible by others (mode %04o); run: chmod 600 %s", s.secretsPath, info.Mode().Perm(), s.secretsPath)
	}
	data, err := os.ReadFile(s.secretsPath)
	if err != nil {
		return syncbus.Secrets{}, err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !variableName.MatchString(name) {
			// The line is not quoted: it may be a secret.
			return syncbus.Secrets{}, fmt.Errorf("%s:%d: expected NAME=value", s.secretsPath, i+1)
		}
		secrets.Values[name] = unquote(strings.TrimSpace(value))
	}
	return secrets, nil
}

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// unquote removes one pair of matching single or double quotes around a value.
// There are no escapes: what is between the quotes is the value.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
