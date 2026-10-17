package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/handoff"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var handoffWriteCommand = &command{
	name:    "handoff write",
	summary: "追加一条交接记录（Handoff Note）：完成了什么、未完成的事项、作者的新指示与风险；会话结束前写入，新会话经 wa-cli next 接续；只能追加（上限 200 条）",
	flags: []flagSpec{
		dirFlag,
		{"file", flagString, "交接记录输入文件（YAML：done 必填，todo 可省略或为空——干净收尾不必硬编待办，instructions 与 risks 可选）；省略时从标准输入读取"},
	},
	outputKeys: []outputKey{{"note", "写入的交接记录：id、writtenAt、done、todo、instructions 与 risks；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runHandoffWrite,
}

var handoffShowCommand = &command{
	name:    "handoff show",
	summary: "只读输出交接记录（Handoff Note）：默认最近一条，--last N 输出最近 N 条（1 到 50），最近在前",
	flags: []flagSpec{
		dirFlag,
		{"last", flagString, "输出最近 N 条（1 到 50，缺省 1）"},
	},
	outputKeys: []outputKey{{"notes", "交接记录数组（最近在前），每项含 id、writtenAt、done、todo、instructions 与 risks"}},
	exitCodes:  standardExitCodes,
	run:        runHandoffShow,
}

type handoffNoteResult struct {
	Result
	Note *handoff.View `json:"note"`
}

type handoffListResult struct {
	Result
	Notes []handoff.View `json:"notes"`
}

func runHandoffWrite(inv *invocation) int {
	dir := projectDir(inv)
	res := &handoffNoteResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var data []byte
		if data, _, problems = readInput(inv, dir); len(problems) == 0 {
			var in handoff.View
			if in, problems = handoff.ParseInput(data); len(problems) == 0 {
				var v *handoff.View
				var changed []string
				if v, changed, problems = handoff.Write(dir, in, now()); v != nil {
					res.Note, res.ChangedPaths = v, changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	handoffNext(res.Diagnostics, dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Note == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已写入交接记录 %s（%d 项完成、%d 项待办）\n", res.Note.ID, len(res.Note.Done), len(res.Note.Todo))
		return err
	})
}

func runHandoffShow(inv *invocation) int {
	dir := projectDir(inv)
	res := &handoffListResult{Notes: []handoff.View{}}
	problems := workspace.Require(dir)
	n := 1
	if v, ok := inv.values["last"]; ok {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 || parsed > 50 {
			problems = []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "last",
				Message: "--last 须为 1 到 50 的整数"}}
		} else {
			n = parsed
		}
	}
	if problems == nil {
		var notes []handoff.View
		if notes, problems = handoff.Show(dir, n); notes != nil {
			res.Notes = notes
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	handoffNext(res.Diagnostics, dir)
	return finish(inv, res, func(w io.Writer) error {
		if len(res.Notes) == 0 {
			fmt.Fprintf(w, "还没有交接记录\n")
			return nil
		}
		for _, note := range res.Notes {
			fmt.Fprintf(w, "%s（%s）：\n", note.ID, note.WrittenAt)
			for _, d := range note.Done {
				fmt.Fprintf(w, "  完成：%s\n", d)
			}
			for _, todo := range note.Todo {
				fmt.Fprintf(w, "  待办：%s\n", todo)
			}
			for _, i := range note.Instructions {
				fmt.Fprintf(w, "  指示：%s\n", i)
			}
			for _, r := range note.Risks {
				fmt.Fprintf(w, "  风险：%s\n", r)
			}
		}
		return nil
	})
}

// handoffNext 为交接记录诊断给出下一步命令。
func handoffNext(ds []Diagnostic, dir string) {
	for i, d := range ds {
		if d.Path != "" {
			continue
		}
		switch d.Code {
		case handoff.CodeTooMany, "artifact.schema-violation", "artifact.parse-error", "handoff.anchored-value":
			ds[i].Next = []string{"wa-cli help handoff write --json"}
		case "input.invalid-value":
			ds[i].Next = []string{"wa-cli help handoff show --json"}
		}
	}
}

var _ = strings.TrimSpace
