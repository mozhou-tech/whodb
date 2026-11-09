package mysql

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/clidey/whodb/core/src/engine"
)

func cleanupStepForTest(t *testing.T, config *engine.PluginConfig, script string) engine.MigrationRecoveryStep {
	t.Helper()
	plan, err := NewMySQLPlugin().PlanMigrationRecovery(config, "", script)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan.Steps {
		if step.DuplicateQuery != "" {
			return step
		}
	}
	t.Fatalf("missing duplicate conflict: %+v", plan)
	return engine.MigrationRecoveryStep{}
}

func TestLiveDuplicateCleanupStrategies(t *testing.T) {
	for _, last := range []bool{false, true} {
		t.Run(fmt.Sprint(last), func(t *testing.T) {
			db, config := recoveryTestDatabase(t)
			_, err := db.Exec("CREATE TABLE duplicates (id BIGINT PRIMARY KEY, tenant INT NULL, account VARCHAR(20) COLLATE utf8mb4_0900_ai_ci NULL); INSERT INTO duplicates VALUES (9007199254740993,1,'One'),(9007199254740994,1,'one'),(3,2,'Two'),(4,2,'two'),(5,3,'One'),(6,1,NULL),(7,1,NULL)")
			if err != nil {
				t.Fatal(err)
			}
			step := cleanupStepForTest(t, config, "CREATE UNIQUE INDEX ux_duplicate ON duplicates(tenant,account)")
			plugin := NewMySQLPlugin()
			preview, err := plugin.CleanMigrationDuplicates(config, step, last, nil)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Groups != 2 || preview.RowsToDelete != 2 || preview.Deleted != 0 || strings.Join(preview.PrimaryKeyColumns, ",") != "id" {
				t.Fatalf("unexpected preview: %+v", preview)
			}
			var count int
			_ = db.QueryRow("SELECT COUNT(*) FROM duplicates").Scan(&count)
			if count != 7 {
				t.Fatal("preview changed records")
			}
			result, err := plugin.CleanMigrationDuplicates(config, step, last, &preview.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if result.Deleted != 2 {
				t.Fatalf("unexpected deletion count: %+v", result)
			}
			ids, err := db.Query("SELECT CAST(id AS CHAR) FROM duplicates ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer ids.Close()
			actual := map[string]bool{}
			for ids.Next() {
				var id string
				if err := ids.Scan(&id); err != nil {
					t.Fatal(err)
				}
				actual[id] = true
			}
			kept, removed := "9007199254740993", "9007199254740994"
			smallKept, smallRemoved := "3", "4"
			if last {
				kept, removed = removed, kept
				smallKept, smallRemoved = smallRemoved, smallKept
			}
			if !actual[kept] || actual[removed] || !actual[smallKept] || actual[smallRemoved] || !actual["5"] || !actual["6"] || !actual["7"] {
				t.Fatalf("wrong retained rows: %+v", actual)
			}
		})
	}
}

func TestLiveDuplicateCleanupRejectsChangedDataAndStrategy(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	_, err := db.Exec("CREATE TABLE duplicates (id INT PRIMARY KEY, account VARCHAR(20));INSERT INTO duplicates VALUES(1,'a'),(2,'a')")
	if err != nil {
		t.Fatal(err)
	}
	step := cleanupStepForTest(t, config, "CREATE UNIQUE INDEX ux_duplicate ON duplicates(account)")
	plugin := NewMySQLPlugin()
	preview, err := plugin.CleanMigrationDuplicates(config, step, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.CleanMigrationDuplicates(config, step, true, &preview.Fingerprint); err == nil {
		t.Fatal("strategy was not bound to preview")
	}
	if _, err := db.Exec("INSERT INTO duplicates VALUES(3,'a')"); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.CleanMigrationDuplicates(config, step, false, &preview.Fingerprint); err == nil {
		t.Fatal("changed data was accepted")
	}
	config.ReadOnly = true
	if _, err := plugin.CleanMigrationDuplicates(config, step, false, &preview.Fingerprint); err == nil {
		t.Fatal("read-only apply was accepted")
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM duplicates").Scan(&count)
	if count != 3 {
		t.Fatal("rejected cleanup changed records")
	}
}

func TestLiveDuplicateCleanupRollsBackForeignKeyFailure(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	_, err := db.Exec("CREATE TABLE duplicates (id INT PRIMARY KEY, account VARCHAR(20));INSERT INTO duplicates VALUES(1,'a'),(2,'a'),(3,'a');CREATE TABLE children(id INT PRIMARY KEY, parent_id INT, FOREIGN KEY(parent_id) REFERENCES duplicates(id));INSERT INTO children VALUES(1,3)")
	if err != nil {
		t.Fatal(err)
	}
	step := cleanupStepForTest(t, config, "CREATE UNIQUE INDEX ux_duplicate ON duplicates(account)")
	plugin := NewMySQLPlugin()
	preview, err := plugin.CleanMigrationDuplicates(config, step, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.CleanMigrationDuplicates(config, step, false, &preview.Fingerprint); err == nil {
		t.Fatal("expected a foreign key failure")
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM duplicates").Scan(&count)
	if count != 3 {
		t.Fatal("partial cleanup was not rolled back")
	}
}

func TestLiveDuplicateCleanupCompositePrimaryKey(t *testing.T) {
	db, config := recoveryTestDatabase(t)
	_, err := db.Exec("CREATE TABLE duplicates (a INT,b INT,account VARCHAR(20),PRIMARY KEY(a,b));INSERT INTO duplicates VALUES(1,9,'a'),(2,1,'a'),(1,2,'a')")
	if err != nil {
		t.Fatal(err)
	}
	step := cleanupStepForTest(t, config, "CREATE UNIQUE INDEX ux_duplicate ON duplicates(account)")
	plugin := NewMySQLPlugin()
	preview, err := plugin.CleanMigrationDuplicates(config, step, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.CleanMigrationDuplicates(config, step, false, &preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	var a, b int
	if err := db.QueryRow("SELECT a,b FROM duplicates").Scan(&a, &b); err != nil || a != 1 || b != 2 {
		t.Fatalf("not ordered by composite PK: %d,%d %v", a, b, err)
	}
}

func TestLiveDuplicateCleanupBlocksSideEffects(t *testing.T) {
	for _, kind := range []string{"cascade", "trigger", "no-key", "non-transactional"} {
		t.Run(kind, func(t *testing.T) {
			db, config := recoveryTestDatabase(t)
			seed := "CREATE TABLE duplicates(id INT PRIMARY KEY,account VARCHAR(20));INSERT INTO duplicates VALUES(1,'a'),(2,'a')"
			if kind == "no-key" {
				seed = strings.Replace(seed, "id INT PRIMARY KEY", "id INT", 1)
			}
			if kind == "non-transactional" {
				seed = strings.Replace(seed, ");INSERT", ") ENGINE=MyISAM;INSERT", 1)
			}
			if _, err := db.ExecContext(context.Background(), seed); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "cascade":
				if _, err := db.Exec("CREATE TABLE children(id INT PRIMARY KEY,parent_id INT,FOREIGN KEY(parent_id) REFERENCES duplicates(id) ON DELETE CASCADE)"); err != nil {
					t.Fatal(err)
				}
			case "trigger":
				if _, err := db.Exec("CREATE TRIGGER delete_hook BEFORE DELETE ON duplicates FOR EACH ROW SET @cleanup_deleted=OLD.id"); err != nil {
					t.Fatal(err)
				}
			}
			step := cleanupStepForTest(t, config, "CREATE UNIQUE INDEX ux_duplicate ON duplicates(account)")
			if _, err := NewMySQLPlugin().CleanMigrationDuplicates(config, step, false, nil); err == nil {
				t.Fatalf("unsafe cleanup accepted: %s", kind)
			}
		})
	}
}
