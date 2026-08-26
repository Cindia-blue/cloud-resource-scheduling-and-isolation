// render-target validates one target record and renders manifest templates
// against it, failing closed on any incomplete field, unresolved
// placeholder, or stale record. It performs no cluster or node I/O itself --
// the record's fields must be supplied already collected fresh by the
// caller (e.g. from `kubectl get node -o json` and a live EBS query).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/IOIsolation/pkg/targetrecord"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "render-target: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		recordJSON string
		outDir     string
		maxAge     time.Duration
		templates  templateList
	)
	flag.StringVar(&recordJSON, "record", "", "path to a target record JSON file")
	flag.StringVar(&outDir, "out", "", "directory to write rendered manifests into")
	flag.DurationVar(&maxAge, "max-age", 20*time.Minute, "reject a record collected longer ago than this")
	flag.Var(&templates, "template", "template file to render (repeatable); output name drops the .tmpl suffix")
	flag.Parse()

	if recordJSON == "" || outDir == "" || len(templates) == 0 {
		return fmt.Errorf("-record, -out, and at least one -template are required")
	}

	raw, err := os.ReadFile(recordJSON)
	if err != nil {
		return fmt.Errorf("read record: %w", err)
	}
	var r targetrecord.Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("parse record: %w", err)
	}

	if err := targetrecord.Validate(r); err != nil {
		return fmt.Errorf("record rejected: %w", err)
	}
	if err := targetrecord.MaxAge(r, time.Now().UTC(), maxAge); err != nil {
		return fmt.Errorf("record rejected: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create out dir: %w", err)
	}
	for _, tmplPath := range templates {
		tmplBytes, err := os.ReadFile(tmplPath)
		if err != nil {
			return fmt.Errorf("read template %s: %w", tmplPath, err)
		}
		rendered, err := targetrecord.RenderPlaceholders(string(tmplBytes), r)
		if err != nil {
			return fmt.Errorf("render %s: %w", tmplPath, err)
		}
		outName := strings.TrimSuffix(filepath.Base(tmplPath), ".tmpl")
		outPath := filepath.Join(outDir, outName)
		if err := os.WriteFile(outPath, []byte(rendered), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		fmt.Println(outPath)
	}
	return nil
}

type templateList []string

func (t *templateList) String() string { return strings.Join(*t, ",") }
func (t *templateList) Set(v string) error {
	*t = append(*t, v)
	return nil
}
