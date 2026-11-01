package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/kw12121212/writing-anything/internal/handoff"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var handoffWriteCommand = &command{
	name:    "handoff write",
	summary: "追加一条交接记录（Handoff Note）：完成了什么、未完成的事项、作者的新指示与风险；会话结束前写入，新会话经 wa-cli next 接续；--unit 把记录归到所属作品（Work），只能追加（上限 200 条）",
	flags: []flagSpec{
		dirFlag,
		{"file", flagString, "交接记录输入文件（YAML：done 必填，todo 可省略或为空——干净收尾不必硬编待办，instructions、risks 与 work 可选，work 为作品 id 且必须存在）；省略时从标准输入读取"},
		{"unit", flagString, "把这条交接记录关联到所属作品（Work）：接受作品 id 或其任一单元的单元引用（Unit Reference），写入时归到所属作品；省略时不关联（记录对所有作品可见）"},
	},
	outputKeys: []outputKey{{"note", "写入的交接记录：id、writtenAt、关联时的 work、done、todo、instructions 与 risks；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runHandoffWrite,
}

var handoffShowCommand = &command{
	name:    "handoff show",
	summary: "只读输出交接记录（Handoff Note）：默认最近一条，--last N 输出最近 N 条（1 到 50），最近在前；--unit 只看关联该作品与未关联的记录",
	flags: []flagSpec{
		dirFlag,
		{"last", flagString, "输出最近 N 条（1 到 50，缺省 1）"},
		{"unit", flagString, "只输出关联到该作品的记录与未关联的记录：接受作品 id 或其任一单元的单元引用（Unit Reference）；省略时输出全部记录"},
	},
	outputKeys: []outputKey{{"notes", "交接记录数组（最近在前），每项含 id、writtenAt、关联时的 work、done、todo、instructions 与 risks"}},
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
				var workID string
				if workID, problems = resolveHandoffWork(dir, inv.values["unit"], in.Work); len(problems) == 0 {
					in.Work = workID
					var v *handoff.View
					var changed []string
					if v, changed, problems = handoff.Write(dir, in, now()); v != nil {
						res.Note, res.ChangedPaths = v, changed
					}
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
		unit := ""
		if res.Note.Work != "" {
			unit = "，关联作品 " + res.Note.Work
		}
		_, err := fmt.Fprintf(w, "已写入交接记录 %s%s（%d 项完成、%d 项待办）\n", res.Note.ID, unit, len(res.Note.Done), len(res.Note.Todo))
		return err
	})
}

// resolveHandoffWork 归一交接记录的作品关联：--unit 接受作品或其任一单元的单元引用（Unit Reference），
// 输入中的 work 只接受作品 id，两者都给出时必须指向同一作品；引用不存在按 work.not-found /
// unit.not-found 报告（field 指向对应参数），冲突报告 input.invalid-value（field 为 unit）。
// 作品存在性在这里而不是 internal/handoff 校验：单元引用解析属于命令接线层（handoff 包不依赖 internal/work）。
func resolveHandoffWork(dir, unit, inputWork string) (string, []workspace.Problem) {
	at := func(ps []workspace.Problem, field string) []workspace.Problem {
		for i := range ps {
			ps[i].Field = field
		}
		return ps
	}
	if unit != "" {
		w, _, ps := work.Resolve(dir, unit)
		if len(ps) > 0 {
			return "", at(ps, "unit")
		}
		if inputWork != "" && inputWork != w.ID {
			return "", []workspace.Problem{{Code: "input.invalid-value", Severity: workspace.SeverityError, Field: "unit",
				Message: "--unit 指向作品（Work）" + w.ID + "，与输入中的 work " + inputWork + " 不一致；两者须指向同一作品或只给其一"}}
		}
		return w.ID, nil
	}
	if inputWork != "" {
		w, _, ps := work.Resolve(dir, inputWork)
		if len(ps) > 0 {
			return "", at(ps, "work")
		}
		return w.ID, nil
	}
	return "", nil
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
	var workID string
	if problems == nil {
		if v, ok := inv.values["unit"]; ok {
			var ps []workspace.Problem
			if workID, ps = resolveHandoffWork(dir, v, ""); len(ps) > 0 {
				problems = ps
			}
		}
	}
	if problems == nil {
		var notes []handoff.View
		if workID != "" {
			notes, problems = handoff.ShowFor(dir, workID, n)
		} else {
			notes, problems = handoff.Show(dir, n)
		}
		if notes != nil {
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
			work := ""
			if note.Work != "" {
				work = "，关联作品 " + note.Work
			}
			fmt.Fprintf(w, "%s（%s%s）：\n", note.ID, note.WrittenAt, work)
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
			// --unit 的取值问题指向 write 的用法，--last 的指向 show。
			if d.Field == "unit" {
				ds[i].Next = []string{"wa-cli help handoff write --json"}
			} else {
				ds[i].Next = []string{"wa-cli help handoff show --json"}
			}
		}
	}
}

var _ = strings.TrimSpace
