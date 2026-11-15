package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// RepoFileName is the config file committed inside the org directory
// itself (see LoadRepo), shared by every machine — and the iOS app —
// that checks the directory out.
const RepoFileName = ".orgtd.toml"

// Layer names, as recorded in Sources and shown by :config.
const (
	LayerFlag    = "flag"
	LayerEnv     = "env"
	LayerUser    = "config"
	LayerRepo    = "repo"
	LayerDefault = "default"
)

// Sources records which layer each setting's effective value came from,
// keyed by its TOML key ("agenda_window_days", "colors.todo_color",
// "keywords", ...). A key that's absent was left at its zero value.
type Sources map[string]string

// Of returns the layer key came from, or "" if none set it.
func (s Sources) Of(key string) string { return s[key] }

// machineLocalKeys are the settings a repo file may not set: they name
// paths or programs on one particular machine, or (gcalsync) hold
// secrets, so they have no business being committed to a shared
// directory. org_dir is also circular: the repo file is found through it.
var machineLocalKeys = []string{"org_dir", "editor", "vcs_editor", "scratch_dir", "debug", "gcalsync"}

// LoadRepo reads dir's RepoFileName, the shared, committed config layer
// (see README's "Config file"). A missing file is not an error and
// yields an empty Config. Machine-local keys (machineLocalKeys) are
// dropped, with one warning each, rather than failing the load: the same
// file is read by other machines and the phone, and one stray key
// shouldn't lock them all out.
func LoadRepo(dir string) (cfg *Config, warnings []string, err error) {
	path := filepath.Join(dir, RepoFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil, nil
		}
		return nil, nil, fmt.Errorf("config: reading %s: %w", path, err)
	}
	return ParseRepo(data, path)
}

// ParseRepo is LoadRepo for already-read file contents; path is only used
// in messages.
func ParseRepo(data []byte, path string) (cfg *Config, warnings []string, err error) {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	for _, key := range machineLocalKeys {
		if _, ok := raw[key]; !ok {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s: ignoring %s (machine-local; set it in your own config file)", path, key))
		v := reflect.ValueOf(&c).Elem()
		f := v.FieldByIndex(fieldIndexByKey(v.Type(), key))
		f.Set(reflect.Zero(f.Type()))
	}
	if err := c.Normalize(); err != nil {
		return nil, nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return &c, warnings, nil
}

// fieldIndexByKey finds the field of struct type t whose toml tag is key.
func fieldIndexByKey(t reflect.Type, key string) []int {
	for i := range t.NumField() {
		if tomlKey(t.Field(i)) == key {
			return []int{i}
		}
	}
	panic("config: no field for key " + key)
}

func tomlKey(f reflect.StructField) string {
	tag, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	return tag
}

// Overlay copies every setting src sets (a non-zero value, per this
// package's zero-means-unset convention) onto dst, recording layer as its
// source in sources (which may be nil). Sections ([colors], [icons],
// [gcalsync]) merge key by key; a list (keywords, outline_ignore_files,
// ...) replaces dst's whole list when non-empty.
func Overlay(dst *Config, src *Config, layer string, sources Sources) {
	if src == nil {
		return
	}
	overlayStruct(reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem(), "", layer, sources)
}

func overlayStruct(dst, src reflect.Value, prefix, layer string, sources Sources) {
	t := dst.Type()
	for i := range t.NumField() {
		key := prefix + tomlKey(t.Field(i))
		d, s := dst.Field(i), src.Field(i)
		if s.Kind() == reflect.Struct {
			overlayStruct(d, s, key+".", layer, sources)
			continue
		}
		if s.IsZero() || (s.Kind() == reflect.Slice && s.Len() == 0) {
			continue
		}
		d.Set(s)
		if sources != nil {
			sources[key] = layer
		}
	}
}

// MarkDefaults records LayerDefault in sources for every setting that is
// set in cfg but has no source yet — i.e. one ApplyDefaults filled in.
func MarkDefaults(cfg *Config, sources Sources) {
	var walk func(v reflect.Value, prefix string)
	walk = func(v reflect.Value, prefix string) {
		t := v.Type()
		for i := range t.NumField() {
			key := prefix + tomlKey(t.Field(i))
			f := v.Field(i)
			if f.Kind() == reflect.Struct {
				walk(f, key+".")
				continue
			}
			if _, ok := sources[key]; !ok && !f.IsZero() {
				sources[key] = LayerDefault
			}
		}
	}
	walk(reflect.ValueOf(cfg).Elem(), "")
}
