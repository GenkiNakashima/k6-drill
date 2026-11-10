package exercise

import (
	"os"
	"testing"
	"testing/fstest"
)

func TestLoadBundledExercises(t *testing.T) {
	set, err := Load(os.DirFS("../../exercises"))
	if err != nil {
		t.Fatal(err)
	}
	basic, advanced, optional := 0, 0, 0
	for _, e := range set.List {
		switch e.Level {
		case "basic":
			basic++
		case "advanced":
			advanced++
		}
		if e.Optional {
			optional++
		}
		if e.HasCode() && e.Solution == "" {
			t.Errorf("%s: solution がありません", e.ID)
		}
		if len(e.Tips) == 0 {
			t.Errorf("%s: 問題文の下に出すヒント（tips）がありません", e.ID)
		}
		for i, tip := range e.Tips {
			if tip.Title == "" || tip.Text == "" {
				t.Errorf("%s: tips[%d] の title か text が空です", e.ID, i)
			}
		}
	}
	if basic != 12 || advanced != 9 || optional != 4 {
		t.Fatalf("basic=%d advanced=%d optional=%d", basic, advanced, optional)
	}
	if set.List[0].ID != "B01" || set.List[len(set.List)-1].ID != "A09" {
		t.Fatalf("order: %s ... %s", set.List[0].ID, set.List[len(set.List)-1].ID)
	}
	if set.Get("A02").Hints[0].When.Kind != "zero_sample_threshold" {
		t.Fatal("A02 hint")
	}
}

func TestValidate(t *testing.T) {
	fsys := fstest.MapFS{
		"basic/B99.yaml": {Data: []byte("id: B98\nlevel: basic\ntitle: x\nminutes: 1\ntype: quiz\nstatement: s\nexplanation: e\ndoc: http://x\nquiz:\n  - question: q\n    choices:\n      - text: a\n        correct: true\n")},
	}
	if _, err := Load(fsys); err == nil {
		t.Fatal("id とファイル名の不一致を見逃した")
	}
	fsys["basic/B99.yaml"] = &fstest.MapFile{Data: []byte("id: B99\nlevel: basic\ntitle: x\nminutes: 1\ntype: code\nstatement: s\nexplanation: e\ndoc: http://x\n")}
	if _, err := Load(fsys); err == nil {
		t.Fatal("starter と judge の欠落を見逃した")
	}
}
