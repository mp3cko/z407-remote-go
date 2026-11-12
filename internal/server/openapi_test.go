package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestOpenAPICoversRoutes keeps docs/openapi.yaml in step with the server:
// every route registered in server.go and every command must be in it.
func TestOpenAPICoversRoutes(t *testing.T) {
	spec, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may have turned the line ends into CRLF.
	spec = []byte(strings.ReplaceAll(string(spec), "\r\n", "\n"))
	routes := regexp.MustCompile(`mux\.HandleFunc\("(GET|POST) (/api/[^"]*)"`).FindAllStringSubmatch(string(src), -1)
	if len(routes) == 0 {
		t.Fatal("no routes found in server.go")
	}
	paths := splitPaths(string(spec))
	for _, r := range routes {
		method, path := strings.ToLower(r[1]), r[2]
		block, ok := paths[path]
		if !ok {
			t.Errorf("%s %s is not in docs/openapi.yaml", r[1], path)
			continue
		}
		if !strings.Contains(block, "\n    "+method+":") {
			t.Errorf("%s %s: the spec has the path but not the method", r[1], path)
		}
	}
	commands := regexp.MustCompile(`"(\w+)":\s+sp\.\w+,`).FindAllStringSubmatch(string(src), -1)
	if len(commands) == 0 {
		t.Fatal("no commands found in server.go")
	}
	for _, c := range commands {
		if !strings.Contains(string(spec), "\n        - "+c[1]+"\n") {
			t.Errorf("command %s is not in the Command enum of docs/openapi.yaml", c[1])
		}
	}
}

// splitPaths returns the text of each path item under "paths:", by path.
func splitPaths(spec string) map[string]string {
	out := map[string]string{}
	key := ""
	for _, line := range strings.Split(spec, "\n") {
		switch {
		case strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":"):
			key = strings.TrimSuffix(strings.TrimSpace(line), ":")
		case line != "" && !strings.HasPrefix(line, "  "):
			key = ""
		}
		if key != "" {
			out[key] += "\n" + line
		}
	}
	return out
}
