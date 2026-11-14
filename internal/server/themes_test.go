package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	tokenBlock = regexp.MustCompile(`(:root[^{]*|\[data-theme="([a-z]+)"\])\s*\{([^}]*)\}`)
	tokenName  = regexp.MustCompile(`(--[a-z0-9-]+)\s*:`)
	hexColour  = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	themeEntry = regexp.MustCompile(`\['([a-z]+)', '[^']+'\]`)
)

func pageStyle(t *testing.T) (style, page string) {
	t.Helper()
	b, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	page = string(b)
	start, end := strings.Index(page, "<style>"), strings.Index(page, "</style>")
	if start < 0 || end < start {
		t.Fatal("no <style> in index.html")
	}
	return page[start:end], page
}

// Every theme the picker offers has its block of tokens, and sets only
// tokens that Classic defines.
func TestThemesComplete(t *testing.T) {
	style, page := pageStyle(t)
	classic := map[string]bool{}
	themes := map[string][]string{}
	for _, m := range tokenBlock.FindAllStringSubmatch(style, -1) {
		names := tokenName.FindAllStringSubmatch(m[3], -1)
		if strings.HasPrefix(m[1], ":root, [data-theme=\"classic\"]") {
			for _, n := range names {
				classic[n[1]] = true
			}
		} else if m[2] != "" {
			for _, n := range names {
				themes[m[2]] = append(themes[m[2]], n[1])
			}
		}
	}
	if len(classic) == 0 {
		t.Fatal("no Classic tokens found")
	}
	_, list, _ := strings.Cut(page, "const THEMES = [")
	list, _, _ = strings.Cut(list, "];")
	offered := themeEntry.FindAllStringSubmatch(list, -1)
	if len(offered) < 2 {
		t.Fatal("no themes offered")
	}
	for _, o := range offered {
		id := o[1]
		if id == "classic" {
			continue
		}
		if _, ok := themes[id]; !ok {
			t.Errorf("theme %q is offered but has no [data-theme] block", id)
		}
	}
	for id, names := range themes {
		for _, n := range names {
			if !classic[n] {
				t.Errorf("theme %q sets %s, which Classic does not define", id, n)
			}
		}
	}
}

// Colours come from the tokens, so that every theme reaches them. White and
// black stay allowed for highlights and masks.
func TestNoColoursOutsideTokens(t *testing.T) {
	style, _ := pageStyle(t)
	rest := tokenBlock.ReplaceAllString(style, "")
	for _, c := range hexColour.FindAllString(rest, -1) {
		if c != "#fff" && c != "#000" {
			t.Errorf("colour %s outside the tokens; add a token for it", c)
		}
	}
}
