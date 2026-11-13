// Package seed - загрузка синтетических начальных данных: YAML-справочники и markdown-база знаний
package seed

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
)

// Load - читает data/seed/*.yaml и data/kb/*.md из каталога dir
func Load(dir string) (domain.Seed, error) {
	var s domain.Seed

	var users struct {
		Users  []domain.User  `yaml:"users"`
		Grants []domain.Grant `yaml:"grants"`
	}
	var services struct {
		Services []domain.Service `yaml:"services"`
	}
	var roles struct {
		Roles []domain.RoleProfile `yaml:"roles"`
	}
	var equipment struct {
		Equipment []domain.Item   `yaml:"equipment"`
		Issued    []domain.Issued `yaml:"issued"`
	}
	var onboarding struct {
		Steps []domain.Step `yaml:"steps"`
	}
	files := map[string]any{
		"users.yaml": &users, "services.yaml": &services, "roles.yaml": &roles,
		"equipment.yaml": &equipment, "onboarding.yaml": &onboarding,
	}
	for name, target := range files {
		if err := readYAML(filepath.Join(dir, "seed", name), target); err != nil {
			return s, err
		}
	}
	s.Users, s.Grants = users.Users, users.Grants
	s.Services = services.Services
	s.Roles = roles.Roles
	s.Items, s.Issued = equipment.Equipment, equipment.Issued
	s.Steps = onboarding.Steps

	docs, err := loadDocs(filepath.Join(dir, "kb"))
	if err != nil {
		return s, err
	}
	s.Docs = docs
	return s, nil
}

// readYAML - строгое чтение: неизвестное поле в справочнике - ошибка, а не тихо пропущенные данные
func readYAML(path string, target any) error {
	data, err := os.ReadFile(path) //nolint:gosec // путь собирается из каталога данных, заданного администратором
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// loadDocs - markdown с YAML front matter (title, service, tags) между строками ---
func loadDocs(dir string) ([]domain.Doc, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	slices.Sort(paths)
	docs := make([]domain.Doc, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // файлы базы знаний из каталога данных
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		doc, err := parseDoc(strings.TrimSuffix(filepath.Base(path), ".md"), string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// parseDoc - разбирает front matter и тело документа
func parseDoc(id, text string) (domain.Doc, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return domain.Doc{}, fmt.Errorf("front matter is missing")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return domain.Doc{}, fmt.Errorf("front matter is not closed")
	}
	var meta struct {
		Title   string   `yaml:"title"`
		Service string   `yaml:"service"`
		Tags    []string `yaml:"tags"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return domain.Doc{}, fmt.Errorf("parse front matter: %w", err)
	}
	if meta.Title == "" {
		return domain.Doc{}, fmt.Errorf("title is required")
	}
	return domain.Doc{ID: id, Title: meta.Title, Service: meta.Service, Tags: meta.Tags, Body: strings.TrimSpace(body)}, nil
}
