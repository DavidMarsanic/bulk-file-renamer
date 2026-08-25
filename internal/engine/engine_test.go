package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestApplyBatch_Swap is the single most important correctness property of
// this app: renaming a.txt->b.txt and b.txt->a.txt in the same batch must
// swap both files' contents, never clobber or lose one. A naive single-pass
// rename would either overwrite b.txt's original content with a.txt's
// before b.txt ever got a chance to move, or fail outright — this is
// exactly what the two-phase apply exists to prevent.
func TestApplyBatch_Swap(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.txt")
	bPath := filepath.Join(dir, "b.txt")

	if err := os.WriteFile(aPath, []byte("A-CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("B-CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := []RenamePair{
		{From: aPath, To: bPath},
		{From: bPath, To: aPath},
	}

	result, err := ApplyBatch(dir, plan)
	if err != nil {
		t.Fatalf("ApplyBatch returned error: %v (errs: %v)", err, result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("ApplyBatch reported errors on success path: %v", result.Errors)
	}
	if result.BatchID == "" {
		t.Fatal("expected a non-empty BatchID")
	}

	aData, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatalf("reading a.txt after swap: %v", err)
	}
	bData, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatalf("reading b.txt after swap: %v", err)
	}

	if string(aData) != "B-CONTENT" {
		t.Errorf("a.txt should now hold B's content, got %q", aData)
	}
	if string(bData) != "A-CONTENT" {
		t.Errorf("b.txt should now hold A's content, got %q", bData)
	}

	// No leftover temp files should remain in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("expected exactly 2 files after swap, got %v", names)
	}

	// The swap must also be undoable, restoring both files to their
	// original names with their original content.
	undone, err := UndoBatch(result.BatchID)
	if err != nil {
		t.Fatalf("UndoBatch returned error: %v (errs: %v)", err, undone.Errors)
	}
	if len(undone.Errors) != 0 {
		t.Fatalf("UndoBatch reported errors on success path: %v", undone.Errors)
	}

	aData, err = os.ReadFile(aPath)
	if err != nil {
		t.Fatalf("reading a.txt after undo: %v", err)
	}
	bData, err = os.ReadFile(bPath)
	if err != nil {
		t.Fatalf("reading b.txt after undo: %v", err)
	}
	if string(aData) != "A-CONTENT" {
		t.Errorf("a.txt should be back to A's content after undo, got %q", aData)
	}
	if string(bData) != "B-CONTENT" {
		t.Errorf("b.txt should be back to B's content after undo, got %q", bData)
	}
}

// TestApplyBatch_ThreeWayCycle exercises a rotation (a->b, b->c, c->a),
// a harder case than a simple two-way swap since it requires every file to
// pass through its temp name before any of the three can land in its final
// spot.
func TestApplyBatch_ThreeWayCycle(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.txt")
	bPath := filepath.Join(dir, "b.txt")
	cPath := filepath.Join(dir, "c.txt")

	os.WriteFile(aPath, []byte("A"), 0o644)
	os.WriteFile(bPath, []byte("B"), 0o644)
	os.WriteFile(cPath, []byte("C"), 0o644)

	plan := []RenamePair{
		{From: aPath, To: bPath},
		{From: bPath, To: cPath},
		{From: cPath, To: aPath},
	}

	result, err := ApplyBatch(dir, plan)
	if err != nil {
		t.Fatalf("ApplyBatch returned error: %v (errs: %v)", err, result.Errors)
	}

	checks := map[string]string{aPath: "C", bPath: "A", cPath: "B"}
	for path, want := range checks {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != want {
			t.Errorf("%s: want content %q, got %q", path, want, got)
		}
	}
}

// TestApplyBatch_RollbackOnFailure verifies that when one rename in the
// plan can't succeed (destination directory doesn't exist), every file is
// left exactly where it started — no partial renames.
func TestApplyBatch_RollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.txt")
	bPath := filepath.Join(dir, "b.txt")

	os.WriteFile(aPath, []byte("A"), 0o644)
	os.WriteFile(bPath, []byte("B"), 0o644)

	plan := []RenamePair{
		{From: aPath, To: filepath.Join(dir, "renamed-a.txt")},
		{From: bPath, To: filepath.Join(dir, "nonexistent-subdir", "renamed-b.txt")},
	}

	result, err := ApplyBatch(dir, plan)
	if err == nil {
		t.Fatal("expected ApplyBatch to return an error")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected result.Errors to be populated")
	}

	if _, err := os.Stat(aPath); err != nil {
		t.Errorf("a.txt should still exist at its original path: %v", err)
	}
	if _, err := os.Stat(bPath); err != nil {
		t.Errorf("b.txt should still exist at its original path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "renamed-a.txt")); err == nil {
		t.Error("renamed-a.txt should not exist — the whole batch should have rolled back")
	}
}

func TestApplyRules_FindReplaceCaseInsensitive(t *testing.T) {
	rules := []Rule{{Type: "find-replace", Find: "IMG", Replace: "Photo", CaseSensitive: false}}
	got, err := ApplyRules("img_0001.jpg", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Photo_0001.jpg" {
		t.Errorf("got %q, want %q", got, "Photo_0001.jpg")
	}
}

func TestApplyRules_FindReplaceCaseSensitive(t *testing.T) {
	rules := []Rule{{Type: "find-replace", Find: "IMG", Replace: "Photo", CaseSensitive: true}}
	got, err := ApplyRules("img_0001.jpg", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Lowercase "img" shouldn't match the case-sensitive "IMG" find.
	if got != "img_0001.jpg" {
		t.Errorf("got %q, want %q (no match expected)", got, "img_0001.jpg")
	}
}

func TestApplyRules_RegexReplace(t *testing.T) {
	rules := []Rule{{Type: "regex-replace", Find: `(\d+)`, Replace: "#$1"}}
	got, err := ApplyRules("track12.mp3", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "track#12.mp3" {
		t.Errorf("got %q, want %q", got, "track#12.mp3")
	}
}

func TestApplyRules_InvalidRegex(t *testing.T) {
	rules := []Rule{{Type: "regex-replace", Find: "(unterminated", Replace: "x"}}
	_, err := ApplyRules("file.txt", rules, 0, time.Time{})
	if err == nil {
		t.Fatal("expected an error for invalid regex")
	}
}

func TestApplyRules_SequencePrefix(t *testing.T) {
	rules := []Rule{{Type: "sequence", Position: "prefix", Start: 1, Padding: 3, Step: 1, Separator: "-"}}
	got, err := ApplyRules("vacation.png", rules, 4, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "005-vacation.png" {
		t.Errorf("got %q, want %q", got, "005-vacation.png")
	}
}

func TestApplyRules_SequenceSuffixStep(t *testing.T) {
	rules := []Rule{{Type: "sequence", Position: "suffix", Start: 10, Padding: 2, Step: 5, Separator: "_"}}
	got, err := ApplyRules("clip.mov", rules, 2, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "clip_20.mov" {
		t.Errorf("got %q, want %q", got, "clip_20.mov")
	}
}

func TestApplyRules_Date(t *testing.T) {
	mod := time.Date(2024, 3, 5, 0, 0, 0, 0, time.UTC)
	rules := []Rule{{Type: "date", Position: "prefix", DateSource: "modified", DateFormat: "2006-01-02", Separator: "_"}}
	got, err := ApplyRules("report.pdf", rules, 0, mod)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2024-03-05_report.pdf" {
		t.Errorf("got %q, want %q", got, "2024-03-05_report.pdf")
	}
}

func TestApplyRules_Case(t *testing.T) {
	cases := []struct {
		mode string
		in   string
		want string
	}{
		{"lower", "MyFile Name", "myfile name.txt"},
		{"upper", "MyFile Name", "MYFILE NAME.txt"},
		{"title", "my_file-name here", "My_File-Name Here.txt"},
	}
	for _, c := range cases {
		rules := []Rule{{Type: "case", CaseMode: c.mode}}
		got, err := ApplyRules(c.in+".txt", rules, 0, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("mode=%s: got %q, want %q", c.mode, got, c.want)
		}
	}
}

func TestApplyRules_Trim(t *testing.T) {
	rules := []Rule{{Type: "trim"}}
	got, err := ApplyRules("  spacey  .txt", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "spacey.txt" {
		t.Errorf("got %q, want %q", got, "spacey.txt")
	}
}

func TestApplyRules_RemoveChars(t *testing.T) {
	rules := []Rule{{Type: "remove-chars", Chars: "()[]"}}
	got, err := ApplyRules("file(1)[final].txt", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "file1final.txt" {
		t.Errorf("got %q, want %q", got, "file1final.txt")
	}
}

func TestApplyRules_Extension(t *testing.T) {
	rules := []Rule{{Type: "extension", NewExtension: ".jpeg"}}
	got, err := ApplyRules("photo.png", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "photo.jpeg" {
		t.Errorf("got %q, want %q", got, "photo.jpeg")
	}
}

func TestApplyRules_ChainOrderAndExtensionUntouched(t *testing.T) {
	rules := []Rule{
		{Type: "find-replace", Find: "IMG", Replace: "Photo", CaseSensitive: true},
		{Type: "case", CaseMode: "lower"},
		{Type: "sequence", Position: "suffix", Start: 1, Padding: 2, Separator: "_"},
	}
	got, err := ApplyRules("IMG_Vacation.JPG", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Extension must retain its original case ("JPG") since no rule in the
	// chain touches the extension.
	if got != "photo_vacation_01.JPG" {
		t.Errorf("got %q, want %q", got, "photo_vacation_01.JPG")
	}
}

func TestPreviewBatch_CollisionDetection(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "IMG_001.jpg")
	f2 := filepath.Join(dir, "img_001.jpg") // differs only by case on a
	// case-sensitive filesystem; both will be forced to the same output
	// name by the rule below, producing a genuine intra-batch collision.

	entries := []FileEntry{
		{Path: f1, Name: "IMG_001.jpg", ModTime: time.Now()},
		{Path: f2, Name: "img_001.jpg", ModTime: time.Now()},
	}
	rules := []Rule{{Type: "case", CaseMode: "lower"}}

	results, err := PreviewBatch(entries, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].Collision || !results[1].Collision {
		t.Errorf("expected both rows to be flagged as colliding, got %+v", results)
	}
}

// TestPreviewBatch_NoFalseCollisionWhenTargetVacatedInBatch covers the
// exception spelled out in the spec: entry0 is renamed onto a path that
// currently belongs to entry1 (still on disk, unmodified, at preview time)
// — but since entry1 is *also* being renamed elsewhere in this same batch,
// that spot will be vacated by the time ApplyBatch actually runs, so this
// must NOT be flagged as a collision. Ordering the two find-replace rules
// so the "move B away" rule runs before the "move A into B's old spot"
// rule keeps each entry's own transformation from re-matching the other
// rule further down the chain, isolating the exact case under test.
func TestPreviewBatch_NoFalseCollisionWhenTargetVacatedInBatch(t *testing.T) {
	dir := t.TempDir()
	onePath := filepath.Join(dir, "one.txt")
	twoPath := filepath.Join(dir, "two.txt")
	os.WriteFile(onePath, []byte("ONE"), 0o644)
	os.WriteFile(twoPath, []byte("TWO"), 0o644)

	entries := []FileEntry{
		{Path: onePath, Name: "one.txt", ModTime: time.Now()},
		{Path: twoPath, Name: "two.txt", ModTime: time.Now()},
	}
	rules := []Rule{
		{Type: "find-replace", Find: "two", Replace: "three", CaseSensitive: true}, // moves "two.txt" out of the way first
		{Type: "find-replace", Find: "one", Replace: "two", CaseSensitive: true},   // then "one.txt" moves into the now-vacated "two.txt"
	}

	results, err := PreviewBatch(entries, rules)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].NewName != "two.txt" || results[1].NewName != "three.txt" {
		t.Fatalf("setup sanity check failed: got %+v", results)
	}
	if results[0].Collision {
		t.Errorf("one.txt->two.txt should not be flagged: two.txt is being vacated by two.txt->three.txt in this same batch, got %+v", results[0])
	}
	if results[1].Collision {
		t.Errorf("two.txt->three.txt should not be flagged: three.txt doesn't exist yet, got %+v", results[1])
	}
}

func TestPreviewBatch_ExternalCollisionIsFlagged(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.txt")
	bPath := filepath.Join(dir, "b.txt")
	os.WriteFile(aPath, []byte("A"), 0o644)
	os.WriteFile(bPath, []byte("B"), 0o644) // untouched by this batch — a genuine obstacle

	// Only a.txt is part of this batch; renaming it to b.txt would clobber
	// a real file that isn't going anywhere.
	entries := []FileEntry{{Path: aPath, Name: "a.txt", ModTime: time.Now()}}
	rules := []Rule{{Type: "find-replace", Find: "a", Replace: "b", CaseSensitive: true}}

	results, err := PreviewBatch(entries, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].Collision {
		t.Errorf("expected external collision (b.txt already exists outside the batch) to be flagged, got %+v", results[0])
	}
}

func TestApplyRules_UnknownRuleTypeIgnored(t *testing.T) {
	rules := []Rule{{Type: "not-a-real-type"}}
	got, err := ApplyRules("file.txt", rules, 0, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "file.txt" {
		t.Errorf("got %q, want unchanged %q", got, "file.txt")
	}
}

func TestListFiles_NonRecursiveSkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o644)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("x"), 0o644)

	entries, err := ListFiles(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "top.txt" {
		t.Errorf("expected only top.txt, got %+v", entries)
	}
}

func TestListFiles_RecursiveIncludesSubdirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0o644)
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("x"), 0o644)

	entries, err := ListFiles(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
}
