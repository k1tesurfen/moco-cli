package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMissingFileGivesDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Schedule.Start != "08:00" || cfg.RoundingMinutes != 15 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestSetSubdomainCreatesValidTemplate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SetSubdomain("artismedia"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Subdomain != "artismedia" || cfg.Schedule.End != "17:00" || cfg.Location.Default != "office" {
		t.Errorf("cfg = %+v", cfg)
	}

	// Changing it keeps the rest of the file.
	if err := SetSubdomain("other"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(Path())
	if !strings.Contains(string(data), `subdomain = "other"`) || !strings.Contains(string(data), "# review =") {
		t.Errorf("file after update:\n%s", data)
	}
}

func TestValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(path, []byte("[schedule]\nstart = \"8:00\"\nsnooze = \"soon\"\n[location]\nmon = \"beach\"\n"), 0o644)
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, want := range []string{"schedule.start", "schedule.snooze", "location.mon"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestWorkdayAndLocation(t *testing.T) {
	cfg := Default()
	cfg.Location.Fri = "home"
	fri := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sat := fri.AddDate(0, 0, 1)
	thu := fri.AddDate(0, 0, -1)
	if !cfg.IsWorkday(fri) || cfg.IsWorkday(sat) {
		t.Error("workday detection wrong")
	}
	if !cfg.HomeOfficeOn(fri) || cfg.HomeOfficeOn(thu) {
		t.Error("location defaults wrong")
	}
}

func TestAliases(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SetSubdomain("x"); err != nil {
		t.Fatal(err)
	}
	if err := SetAlias("intern", Alias{Project: `Intern – "nicht" verrechenbar`, Task: "Programmierung"}); err != nil {
		t.Fatal(err)
	}
	if err := SetAlias("seo", Alias{Project: "Website", Task: "SEO"}); err != nil {
		t.Fatal(err)
	}
	if err := SetAlias("intern", Alias{Project: "Intern", Task: "Konzept"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Aliases) != 2 || cfg.Aliases["intern"].Task != "Konzept" || cfg.Aliases["seo"].Project != "Website" {
		t.Errorf("aliases = %+v", cfg.Aliases)
	}
	if err := RemoveAlias("seo"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAlias("seo"); err == nil {
		t.Error("removing a missing alias should fail")
	}
	data, _ := os.ReadFile(Path())
	if !strings.Contains(string(data), "# review =") || strings.Contains(string(data), "seo") {
		t.Errorf("file:\n%s", data)
	}
	if err := SetAlias("Bad Name", Alias{}); err == nil {
		t.Error("expected invalid name error")
	}
}

func TestAliasesSectionMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.MkdirAll(Dir(), 0o755)
	os.WriteFile(Path(), []byte("subdomain = \"x\"\n"), 0o644)
	if err := SetAlias("a", Alias{Project: "P", Task: "T"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.Aliases["a"].Task != "T" {
		t.Errorf("cfg = %+v, %v", cfg.Aliases, err)
	}
}
