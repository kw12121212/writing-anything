package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// donePost 把 my-post 的 work.yaml 改为 stage: done 并写入根单元正文。
func donePost(t *testing.T, dir, draft string) {
	t.Helper()
	writeFile(t, workFile(dir, "my-post"), "schema_version: 1\nid: my-post\ngenre: minimal\ntype: article\nstage: done\nunits: []\n")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", "my-post", "draft.md"), draft)
}

func canonFile(dir, work string) string {
	return filepath.Join(dir, ".writing-anything", "works", work, "canon.yaml")
}

func TestRecallKeyword(t *testing.T) {
	dir := newWork(t)
	donePost(t, dir, "开头一段。\n\n守灯人把灯点亮。")
	code, got := runJSON(t, "recall", "守灯人", "--dir", dir, "--json")
	if code != 0 || got["valid"] != true {
		t.Fatalf("exit code = %d, valid = %v, diagnostics = %+v", code, got["valid"], got["diagnostics"])
	}
	if arr, ok := got["changedPaths"].([]any); !ok || len(arr) != 1 || arr[0] != ".writing-anything/.recall-index.yaml" {
		t.Errorf("changedPaths = %#v, want the index cache", got["changedPaths"])
	}
	if arr, ok := got["indexed"].([]any); !ok || len(arr) != 1 || arr[0] != "my-post" {
		t.Errorf("indexed = %#v", got["indexed"])
	}
	results, ok := got["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %#v", got["results"])
	}
	h := results[0].(map[string]any)
	if h["work"] != "my-post" || h["unit"] != "my-post" || h["paragraph"] != float64(2) || h["entry"] != nil || h["matched"] != "守灯人" {
		t.Errorf("hit = %#v", h)
	}
	if sn, ok := h["snippet"].(string); !ok || !strings.Contains(sn, "守灯人") {
		t.Errorf("snippet = %#v", h["snippet"])
	}
	// 内容未变：再次检索不写盘、不重建。
	_, got = runJSON(t, "recall", "守灯人", "--dir", dir, "--json")
	if arr, ok := got["changedPaths"].([]any); !ok || len(arr) != 0 {
		t.Errorf("second run changedPaths = %#v, want empty", got["changedPaths"])
	}
	if arr, ok := got["indexed"].([]any); !ok || len(arr) != 0 {
		t.Errorf("second run indexed = %#v, want empty", got["indexed"])
	}
}

func TestRecallText(t *testing.T) {
	dir := newWork(t)
	donePost(t, dir, "开头一段。\n\n守灯人把灯点亮。")
	code, stdout, stderr := run(t, "recall", "守灯人", "--dir", dir)
	if code != 0 || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "my-post 第 2 段") || !strings.Contains(stdout, "守灯人") {
		t.Errorf("stdout = %q", stdout)
	}
	code, stdout, _ = run(t, "recall", "无踪之物", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "没有在已验收正文中找到") {
		t.Errorf("no-hit stdout = %q, exit code = %d", stdout, code)
	}
}

func TestRecallCanonAliases(t *testing.T) {
	dir := newWork(t)
	donePost(t, dir, "守灯人守着灯。\n\n夜里只有老灯匠。")
	if code, stdout, stderr := run(t, "canon", "add", "my-post", "--type", "character", "--name", "守灯人",
		"--aliases", "老灯匠,提灯人", "--text", "五十岁", "--dir", dir); code != 0 {
		t.Fatalf("canon add: exit code = %d; stdout: %s; stderr: %s", code, stdout, stderr)
	}
	// 条目 id 与别名都解析到同一条目：命中标注条目 id，两段各命中一次。
	for _, q := range []string{"canon-1", "老灯匠"} {
		code, got := runJSON(t, "recall", q, "--dir", dir, "--json")
		if code != 0 {
			t.Fatalf("recall %s: exit code = %d, diagnostics = %+v", q, code, got["diagnostics"])
		}
		results := got["results"].([]any)
		if len(results) != 2 {
			t.Fatalf("recall %s: results = %#v", q, got["results"])
		}
		first := results[0].(map[string]any)
		if first["entry"] != "canon-1" || first["matched"] != "守灯人" {
			t.Errorf("recall %s: first hit = %#v", q, first)
		}
	}
}

func TestRecallWorkScope(t *testing.T) {
	dir := newWork(t)
	donePost(t, dir, "守灯人。")
	if code, stdout, stderr := run(t, "new", "minimal", "other-post", "--dir", dir); code != 0 {
		t.Fatalf("new: exit code = %d; stdout: %s; stderr: %s", code, stdout, stderr)
	}
	donePostWork(t, dir, "other-post", "守灯人。")
	code, got := runJSON(t, "recall", "守灯人", "--work", "my-post", "--dir", dir, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, diagnostics = %+v", code, got["diagnostics"])
	}
	results := got["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["work"] != "my-post" {
		t.Errorf("results = %#v, want only my-post", got["results"])
	}
}

// donePostWork 把作品 work 的 work.yaml 改为 stage: done 并写入根单元正文。
func donePostWork(t *testing.T, dir, work, draft string) {
	t.Helper()
	writeFile(t, workFile(dir, work), "schema_version: 1\nid: "+work+"\ngenre: minimal\ntype: article\nstage: done\nunits: []\n")
	writeFile(t, filepath.Join(dir, ".writing-anything", "works", work, "draft.md"), draft)
}

func TestRecallEmptyQuery(t *testing.T) {
	dir := newWork(t)
	code, got := runJSON(t, "recall", " ", "--dir", dir, "--json")
	if code != 1 || got["valid"] != false {
		t.Fatalf("exit code = %d, valid = %v", code, got["valid"])
	}
	ds := got["diagnostics"].([]any)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %#v", got["diagnostics"])
	}
	d := ds[0].(map[string]any)
	if d["code"] != "input.invalid-value" || d["field"] != "query" {
		t.Errorf("diagnostic = %#v", d)
	}
	if arr, ok := got["results"].([]any); !ok || len(arr) != 0 {
		t.Errorf("results = %#v, want empty array on failure (shared-envelope)", got["results"])
	}
	if arr, ok := got["indexed"].([]any); !ok || len(arr) != 0 {
		t.Errorf("indexed = %#v, want empty array on failure (shared-envelope)", got["indexed"])
	}
}

func TestRecallInvalidCanonDegrades(t *testing.T) {
	dir := newWork(t)
	donePost(t, dir, "守灯人守着灯。")
	writeFile(t, canonFile(dir, "my-post"), "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    text: a\n    sources: []\n    protection: normal\n"+
		"  - id: canon-1\n    type: character\n    name: 重复\n    text: b\n    sources: []\n    protection: normal\n")
	code, got := runJSON(t, "recall", "守灯人", "--dir", dir, "--json")
	if code != 1 || got["valid"] != false {
		t.Fatalf("exit code = %d, valid = %v", code, got["valid"])
	}
	if ds, ok := got["diagnostics"].([]any); !ok || len(ds) == 0 {
		t.Fatalf("diagnostics = %#v, want canon.yaml problems", got["diagnostics"])
	}
	// 坏 canon 的作品跳过别名解析，但正文仍按原词检索。
	results := got["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["entry"] != nil {
		t.Errorf("results = %#v", got["results"])
	}
}

func TestRecallHelp(t *testing.T) {
	code, got := runJSON(t, "help", "recall", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	cs := got["commands"].([]any)
	if len(cs) != 1 {
		t.Fatalf("commands = %#v", got["commands"])
	}
	c := cs[0].(map[string]any)
	if c["name"] != "recall" {
		t.Fatalf("command name = %#v", c["name"])
	}
	args := c["args"].([]any)
	if len(args) != 1 || args[0].(map[string]any)["name"] != "query" || args[0].(map[string]any)["required"] != true {
		t.Errorf("args = %#v", args)
	}
	var flags []string
	for _, f := range c["flags"].([]any) {
		flags = append(flags, f.(map[string]any)["name"].(string))
	}
	for _, want := range []string{"dir", "work", "json", "help"} {
		found := false
		for _, f := range flags {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("flags = %v, want %s", flags, want)
		}
	}
	var keys []string
	for _, k := range c["outputKeys"].([]any) {
		keys = append(keys, k.(map[string]any)["name"].(string))
	}
	for _, want := range []string{"results", "indexed"} {
		found := false
		for _, k := range keys {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("outputKeys = %v, want %s", keys, want)
		}
	}
}

func TestCanonAliasesCrud(t *testing.T) {
	dir := newWork(t)
	// item（物品）条目带别名。
	code, got := runJSON(t, "canon", "add", "my-post", "--type", "item", "--name", "长命灯",
		"--aliases", "青焰灯,灯", "--text", "藏在阁楼的旧灯", "--dir", dir, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, diagnostics = %+v", code, got["diagnostics"])
	}
	c := got["canon"].(map[string]any)
	if c["type"] != "item" || c["name"] != "长命灯" {
		t.Fatalf("canon = %#v", c)
	}
	if aliases, ok := c["aliases"].([]any); !ok || len(aliases) != 2 || aliases[0] != "青焰灯" || aliases[1] != "灯" {
		t.Errorf("aliases = %#v", c["aliases"])
	}
	// 与已有条目的 name 重复：拒绝且不写文件。
	code, got = runJSON(t, "canon", "add", "my-post", "--type", "character", "--name", "老周",
		"--aliases", "长命灯", "--text", "描述", "--dir", dir, "--json")
	if code != 1 {
		t.Fatalf("duplicate alias accepted: exit code = %d", code)
	}
	d := got["diagnostics"].([]any)[0].(map[string]any)
	if d["code"] != "canon.duplicate-alias" || d["field"] != "aliases[0]" {
		t.Errorf("diagnostic = %#v", d)
	}
	// set 整体替换别名。
	code, got = runJSON(t, "canon", "set", "my-post", "canon-1", "--aliases", "老灯匠", "--dir", dir, "--json")
	if code != 0 {
		t.Fatalf("canon set: exit code = %d, diagnostics = %+v", code, got["diagnostics"])
	}
	if c := got["canon"].(map[string]any); len(c["aliases"].([]any)) != 1 || c["aliases"].([]any)[0] != "老灯匠" {
		t.Errorf("aliases = %#v", got["canon"])
	}
	// 取值未变不写文件。
	_, got = runJSON(t, "canon", "set", "my-post", "canon-1", "--aliases", "老灯匠", "--dir", dir, "--json")
	if arr, ok := got["changedPaths"].([]any); !ok || len(arr) != 0 {
		t.Errorf("unchanged set changedPaths = %#v", got["changedPaths"])
	}
	// 空字符串删除该字段。
	_, got = runJSON(t, "canon", "set", "my-post", "canon-1", "--aliases", "", "--dir", dir, "--json")
	if c := got["canon"].(map[string]any); len(c["aliases"].([]any)) != 0 {
		t.Errorf("cleared aliases = %#v", c["aliases"])
	}
	// 修改后的别名与其他条目重复：拒绝。
	run(t, "canon", "add", "my-post", "--type", "character", "--name", "黑塔", "--text", "要塞", "--dir", dir)
	code, got = runJSON(t, "canon", "set", "my-post", "canon-1", "--aliases", "黑塔", "--dir", dir, "--json")
	if code != 1 {
		t.Fatalf("duplicate alias on set accepted: exit code = %d", code)
	}
	if d := got["diagnostics"].([]any)[0].(map[string]any); d["code"] != "canon.duplicate-alias" {
		t.Errorf("diagnostic = %#v", d)
	}
	// 别名元素只含空白：按 Schema 违规拒绝，不写文件。
	code, got = runJSON(t, "canon", "add", "my-post", "--type", "character", "--name", "甲", "--aliases", "  ", "--text", "描述", "--dir", dir, "--json")
	if code != 1 {
		t.Fatalf("blank alias accepted: exit code = %d", code)
	}
	if d := got["diagnostics"].([]any)[0].(map[string]any); d["code"] != "artifact.schema-violation" || d["field"] != "aliases" {
		t.Errorf("diagnostic = %#v", d)
	}
	code, got = runJSON(t, "canon", "set", "my-post", "canon-1", "--aliases", " , ", "--dir", dir, "--json")
	if code != 1 {
		t.Fatalf("blank alias on set accepted: exit code = %d", code)
	}
	if d := got["diagnostics"].([]any)[0].(map[string]any); d["code"] != "artifact.schema-violation" || d["field"] != "aliases" {
		t.Errorf("diagnostic = %#v", d)
	}
	// world-rule 不得带别名。
	code, got = runJSON(t, "canon", "add", "my-post", "--type", "world-rule", "--text", "规则", "--aliases", "灯油", "--dir", dir, "--json")
	if code != 1 {
		t.Fatalf("alias on world-rule accepted: exit code = %d", code)
	}
	if d := got["diagnostics"].([]any)[0].(map[string]any); d["code"] != "artifact.schema-violation" || d["field"] != "aliases" {
		t.Errorf("diagnostic = %#v", d)
	}
	// list 输出别名。
	run(t, "canon", "set", "my-post", "canon-1", "--aliases", "青焰灯", "--dir", dir)
	_, got = runJSON(t, "canon", "list", "my-post", "--dir", dir, "--json")
	entries := got["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", got["entries"])
	}
	first := entries[0].(map[string]any)
	if aliases, ok := first["aliases"].([]any); !ok || len(aliases) != 1 || aliases[0] != "青焰灯" {
		t.Errorf("list aliases = %#v", first["aliases"])
	}
	if second := entries[1].(map[string]any); len(second["aliases"].([]any)) != 0 {
		t.Errorf("list aliases without field = %#v", second["aliases"])
	}
}

func TestCanonValidateDuplicateAlias(t *testing.T) {
	dir := newWork(t)
	writeFile(t, canonFile(dir, "my-post"), "schema_version: 1\nentries:\n"+
		"  - id: canon-1\n    type: character\n    name: 守灯人\n    text: a\n    sources: []\n    protection: normal\n"+
		"  - id: canon-2\n    type: location\n    name: 灯塔\n    aliases: [守灯人]\n    text: b\n    sources: []\n    protection: normal\n")
	code, got := runJSON(t, "validate", "--dir", dir, "--json")
	if code != 1 || got["valid"] != false {
		t.Fatalf("exit code = %d, valid = %v", code, got["valid"])
	}
	d := got["diagnostics"].([]any)[0].(map[string]any)
	if d["code"] != "canon.duplicate-alias" || d["field"] != "entries[1].aliases[0]" {
		t.Errorf("diagnostic = %#v", d)
	}
}
