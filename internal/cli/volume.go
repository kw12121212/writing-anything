package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/volume"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var volumeCheckpointCommand = &command{
	name:    "volume checkpoint",
	summary: "生成部（如 part）的卷末检查点（Volume Checkpoint）：本部全部场景验收后，汇总本部新增设定、已确认的状态变化、伏笔变化与未处理决策，并确定性抽取收口条目供作者对照原文核对",
	args: []argSpec{
		{"part", "部单元（Unit）引用；必须是声明 stage_from_children 的汇总单元（如 part）", true},
	},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"checkpoint", "生成的检查点：part、path、scene、canon、states、threads、decisions、sampled、confirmedByAuthor、confirmation 与 confirmedAt；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runVolumeCheckpoint,
}

var volumeConfirmCommand = &command{
	name:    "volume confirm",
	summary: "记录作者对部卷末检查点（Volume Checkpoint）的确认：--confirmed-by-author 与 --confirmation 必须同给；strict 关卡下未经确认，下一部的场景不能验收",
	args: []argSpec{
		{"part", "部单元（Unit）引用", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"confirmed-by-author", flagBool, "作者确认本部的卷末检查点"},
		{"confirmation", flagString, "作者确认原话摘要"},
	},
	outputKeys: []outputKey{{"checkpoint", "确认后的检查点；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runVolumeConfirm,
}

type volumeResult struct {
	Result
	Checkpoint *volume.View `json:"checkpoint"`
}

func runVolumeCheckpoint(inv *invocation) int {
	dir := projectDir(inv)
	res := &volumeResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var v *volume.View
			var changed []string
			if v, changed, problems = volume.Checkpoint(dir, w, u, now()); v != nil {
				res.Checkpoint, res.ChangedPaths = v, changed
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	volumeNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Checkpoint == nil {
			return nil
		}
		_, err := io.WriteString(w, volumeText(res.Checkpoint))
		return err
	})
}

func runVolumeConfirm(inv *invocation) int {
	dir := projectDir(inv)
	res := &volumeResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			if !inv.flag("confirmed-by-author") || strings.TrimSpace(inv.values["confirmation"]) == "" {
				problems = []workspace.Problem{{Code: volume.CodeIncompleteConfirmation, Severity: workspace.SeverityError, Field: "confirmation",
					Message: "作者确认必须给出 --confirmed-by-author 与非空白的 --confirmation（作者原话摘要）"}}
			} else {
				var v *volume.View
				var changed []string
				if v, changed, problems = volume.Confirm(dir, w, u, inv.values["confirmation"], now()); v != nil {
					res.Checkpoint, res.ChangedPaths = v, changed
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	volumeNext(res.Diagnostics, inv.args[0], dir)
	return finish(inv, res, func(w io.Writer) error {
		if res.Checkpoint == nil {
			return nil
		}
		_, err := fmt.Fprintf(w, "已确认部 %s 的卷末检查点（Volume Checkpoint）：%s（作者：%s）\n",
			res.Checkpoint.Part, res.Checkpoint.Path, deref(res.Checkpoint.Confirmation))
		return err
	})
}

// volumeText 返回检查点的人读文本。
func volumeText(v *volume.View) string {
	var b strings.Builder
	verb := "已生成"
	if len(v.Decisions) > 0 {
		verb = "已生成（注意：还有未处理的决策）"
	}
	fmt.Fprintf(&b, "%s部 %s 的卷末检查点（Volume Checkpoint）：%s（最后场景 %s）\n", verb, v.Part, v.Path, v.Scene)
	list := func(label string, ids []string) {
		if len(ids) > 0 {
			fmt.Fprintf(&b, "  %s：%s\n", label, strings.Join(ids, "、"))
		}
	}
	list("新增设定（Canon）", v.Canon)
	list("已确认的状态变化", v.States)
	list("伏笔变化", v.Threads)
	list("未处理决策", v.Decisions)
	for _, s := range v.Sampled {
		fmt.Fprintf(&b, "  请核对：%s 的%s提交 %s：%s\n", s.Scene, kindText(s.Kind), s.ID, s.Text)
	}
	if v.ConfirmedByAuthor {
		fmt.Fprintf(&b, "  作者已确认：%s\n", deref(v.Confirmation))
	} else {
		b.WriteString("  待作者确认：wa-cli volume confirm\n")
	}
	return b.String()
}

func kindText(kind string) string {
	if kind == "state" {
		return "状态变化"
	}
	return "知情"
}

// volumeNext 为卷末检查点的诊断给出下一步命令。
func volumeNext(ds []Diagnostic, ref, dir string) {
	for i, d := range ds {
		switch d.Code {
		case volume.CodeScenesOpen:
			ds[i].Next = []string{shellCommand("wa-cli", "status", "--dir", dir, "--json"), "wa-cli help volume checkpoint --json"}
		case volume.CodeNotVolume:
			ds[i].Next = []string{"wa-cli help volume checkpoint --json"}
		case volume.CodeIncompleteConfirmation:
			ds[i].Next = []string{"wa-cli help volume confirm --json"}
		case volume.CodeNotFound:
			ds[i].Next = []string{"wa-cli help volume checkpoint --json",
				shellCommand("wa-cli", "volume", "checkpoint", ref, "--dir", dir, "--json")}
		case volume.CodeAlreadyConfirmed:
			ds[i].Next = []string{shellCommand("wa-cli", "volume", "checkpoint", ref, "--dir", dir, "--json")}
		}
	}
}
