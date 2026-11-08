package flyway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFlywayDeduplicatedExecution(t *testing.T) {
	binary := os.Getenv("WHODB_TEST_FLYWAY_BINARY")
	if binary == "" {
		t.Skip("set WHODB_TEST_FLYWAY_BINARY for a real Flyway/H2 execution test")
	}
	root := t.TempDir()
	for _, dir := range []string{"source", "copy"} {
		path := filepath.Join(root, dir)
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "V1__init.sql"), []byte("CREATE TABLE dedup_check (id INT);"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run := func(location string) ([]byte, error) {
		return exec.CommandContext(ctx, binary, "-url=jdbc:h2:mem:whodb_dedup_test", "-locations=filesystem:"+location, "migrate").CombinedOutput()
	}
	output, err := run(root)
	if err == nil || !strings.Contains(string(output), "more than one migration") {
		t.Fatalf("duplicate repro failed: %v\n%s", err, output)
	}
	staged, err := prepareMigrationDirectory(InspectDirectory(root))
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staged)
	if output, err := run(staged); err != nil {
		t.Fatalf("deduplicated migration failed: %v\n%s", err, output)
	}
}

func TestProjectMigrationsExcludeBuildCopies(t *testing.T) {
	root := t.TempDir()
	for _, relative := range []string{
		"module/src/main/resources/db/migration/platform/V20260908_1__add_bot.sql",
		"module/target/classes/db/migration/platform/V20260908_1__add_bot.sql",
	} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("SELECT 1;"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	preview := InspectDirectory(root)
	if !preview.Exists || len(preview.Files) != 1 || strings.Contains(preview.Files[0].RelativePath, "target") {
		t.Fatalf("expected one source migration, got %+v", preview)
	}
	staged, err := prepareMigrationDirectory(preview)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staged)
	if got := InspectDirectory(staged); len(got.Files) != 1 {
		t.Fatalf("execution files differ from preview: %+v", got)
	}
	selected := filepath.Join(root, "module/target/classes/db/migration/platform")
	if got := InspectDirectory(selected); len(got.Files) != 1 {
		t.Fatalf("explicit build directory must remain usable: %+v", got)
	}
}

func TestPlainMigrationDirectoryIsPreserved(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "V1__init.sql"), []byte("SELECT 1;"), 0644); err != nil {
		t.Fatal(err)
	}
	if preview := InspectDirectory(root); len(preview.Files) != 1 {
		t.Fatalf("expected plain SQL migration: %+v", preview)
	}

}

func TestDuplicateMigrationFiles(t *testing.T) {
	for _, test := range []struct {
		name         string
		secondName   string
		secondSQL    string
		secondConfig string
		want         int
	}{
		{name: "identical copies", secondName: "V1__init.sql", secondSQL: "SELECT 1;", want: 1},
		{name: "conflicting SQL remains visible", secondName: "V1__init.sql", secondSQL: "SELECT 2;", want: 2},
		{name: "different names remain visible", secondName: "V1__other.sql", secondSQL: "SELECT 1;", want: 2},
		{name: "different script configuration remains visible", secondName: "V1__init.sql", secondSQL: "SELECT 1;", secondConfig: "executeInTransaction=false", want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			inputs := map[string]string{"source/V1__init.sql": "SELECT 1;", "copy/" + test.secondName: test.secondSQL}
			if test.secondConfig != "" {
				inputs["copy/"+test.secondName+".conf"] = test.secondConfig
			}
			for relative, content := range inputs {
				path := filepath.Join(root, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			preview := InspectDirectory(root)
			if preview.Error != "" || len(preview.Files) != test.want {
				t.Fatalf("scan = %+v, want %d files", preview, test.want)
			}
			staged, err := prepareMigrationDirectory(preview)
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(staged)
			actual := InspectDirectory(staged)
			if len(actual.Files) != test.want {
				t.Fatalf("execution includes unexpected files: %+v", actual)
			}
			for relative, content := range inputs {
				got, err := os.ReadFile(filepath.Join(root, relative))
				if err != nil || string(got) != content {
					t.Fatalf("source modified: %s", relative)
				}
			}
			if test.secondConfig != "" {
				got, err := os.ReadFile(filepath.Join(staged, "copy", test.secondName+".conf"))
				if err != nil || string(got) != test.secondConfig {
					t.Fatalf("script configuration lost: %s, %v", got, err)
				}
			}
		})
	}
}
