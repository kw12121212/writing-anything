package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kw12121212/writing-anything/internal/closeout"
	"github.com/kw12121212/writing-anything/internal/outline"
	"github.com/kw12121212/writing-anything/internal/state"
	"github.com/kw12121212/writing-anything/internal/threads"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var sceneCloseOutCommand = &command{
	name:    "scene close-out",
	summary: "写后收口（Close-out）：场景草拟后从 --file 或标准输入提交抽取结果（新增设定、状态变化、知情变化、伏笔埋设与回收、确认产生的事件），对照场景契约检查齐备后把记忆台账与正文同步；同时扫描疑似专有名词提示补录或豁免；--waive 只把名字并入豁免名单",
	args: []argSpec{
		{"unit", "场景单元（Unit）引用；必须是 scene 类型", true},
	},
	flags: []flagSpec{
		dirFlag,
		{"file", flagString, "收口输入文件（YAML，相对 --dir 或绝对路径）；省略时从标准输入读取"},
		{"replace", flagBool, "closeout.yaml 已存在时整体替换重收口；不清理也不撤销已写入的台账条目"},
		{"waive", flagList, "豁免一个疑似专有名词（Suspected Proper Noun），可重复给出；给出时不读收口输入，只更新收口记录的 waived 名单"},
	},
	outputKeys: []outputKey{{"closeout", "收口结果：unit、path、draftSha256、canon、state（含 contract 关联）、knowledge、plants、recovers 各写入的条目 id、events、waived 与扫描命中的 suspected 名单；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runSceneCloseOut,
}

type closeoutResult struct {
	Result
	Closeout *closeout.View `json:"closeout"`
}

func runSceneCloseOut(inv *invocation) int {
	dir := projectDir(inv)
	res := &closeoutResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			switch {
			case u.Type != outline.SceneType:
				problems = []workspace.Problem{{Code: outline.CodeNotScene, Severity: workspace.SeverityError, Field: "unit",
					Message: fmt.Sprintf("单元 %s 的类型是 %s；scene close-out 只收口 scene 类型单元（场景）", u.Ref, u.Type)}}
			case len(inv.lists["waive"]) > 0:
				var v *closeout.View
				var changed []string
				if v, changed, problems = closeout.Waive(dir, w, u, inv.lists["waive"]); v != nil {
					res.Closeout, res.ChangedPaths = v, changed
				}
			default:
				var data []byte
				if data, _, problems = readInput(inv, dir); len(problems) == 0 {
					var in closeout.Input
					if in, problems = closeout.ParseInput(data); len(problems) == 0 {
						// Submit 的第三个返回值在失败时是问题、成功时是疑似专有名词 warning，两种都要接住。
						var v *closeout.View
						var changed []string
						if v, changed, problems = closeout.Submit(dir, w, u, in, inv.flag("replace")); v != nil {
							res.Closeout, res.ChangedPaths = v, changed
						}
					}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	closeoutNext(res.Diagnostics, dir, inv.args[0])
	return finish(inv, res, func(w io.Writer) error {
		if res.Closeout == nil {
			return nil
		}
		_, err := io.WriteString(w, closeoutText(res.Closeout))
		return err
	})
}

// closeoutText 返回收口结果的人读文本。
func closeoutText(v *closeout.View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "已收口（Close-out）场景 %s：%s（正文指纹 %s）\n", v.Unit, v.Path, v.DraftSHA256[:12])
	writeIDs := func(label string, ids []string) {
		if len(ids) > 0 {
			fmt.Fprintf(&b, "  %s：%s\n", label, strings.Join(ids, "、"))
		}
	}
	writeIDs("新增设定（Canon）", v.Canon)
	writeIDs("状态变化提议", idsOfRefs(v.State))
	writeIDs("知情提议", v.Knowledge)
	writeIDs("伏笔埋设", v.Plants)
	writeIDs("伏笔回收", v.Recovers)
	writeIDs("确认产生的事件（Event）", v.Events)
	if len(v.Waived) > 0 {
		fmt.Fprintf(&b, "  已豁免的疑似专有名词：%s\n", strings.Join(v.Waived, "、"))
	}
	for _, s := range v.Suspected {
		fmt.Fprintf(&b, "  疑似专有名词（%s）：%s；请 wa-cli canon add 补录，或用 --waive %s 豁免\n", suspectedSourceText(s.Source), s.Name, s.Name)
	}
	return b.String()
}

func idsOfRefs(refs []closeout.StateRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

func suspectedSourceText(source string) string {
	switch source {
	case "pov":
		return "视角人物"
	case "location":
		return "地点"
	case closeout.SourceDraft:
		return "正文"
	}
	return "出场人物"
}

// closeoutNext 为收口的诊断给出下一步命令：由收口输入引起、不填 path 的诊断先给出该场景的大纲（场景契约），
// 再给出 scene close-out 与相关命令的用法；单元不是场景或参数取值不合法时先给出 wa-cli status；
// warning 的疑似专有名词先给出设定集，再指向 canon add 用法与豁免方法。既有产物中的问题保留默认的复查命令。
func closeoutNext(ds []Diagnostic, dir, ref string) {
	work := ref
	if w, _, found := strings.Cut(ref, "/"); found {
		work = w
	}
	contract := shellCommand("wa-cli", "outline", "show", ref, "--dir", dir, "--json")
	canonList := shellCommand("wa-cli", "canon", "list", work, "--dir", dir, "--json")
	const help = "wa-cli help scene close-out --json"
	for i, d := range ds {
		if d.Code == closeout.CodeSuspectedName {
			ds[i].Next = []string{canonList, "wa-cli help canon add --json", help}
			continue
		}
		if d.Path != "" {
			continue
		}
		switch d.Code {
		case closeout.CodeMissingStateChange, closeout.CodeMissingRecovery, closeout.CodeMissingPlant, closeout.CodeMissingEvent:
			ds[i].Next = []string{contract, help, "wa-cli help scene contract --json"}
		case closeout.CodeUnknownEvent, closeout.CodeAlreadyClosed, closeout.CodeNotClosed, closeout.CodeInvalid,
			"artifact.schema-violation", "artifact.parse-error", "artifact.duplicate-id",
			threads.CodeUnknownUnit, threads.CodeRecoverBeforePlant:
			ds[i].Next = []string{contract, help}
		case state.CodeUnknownEntry, "knowledge.unknown-entry", "knowledge.unknown-character":
			ds[i].Next = []string{canonList, help}
		case threads.CodeNotFound:
			ds[i].Next = []string{
				shellCommand("wa-cli", "threads", work, "--all", "--dir", dir, "--json"),
				help,
			}
		case outline.CodeNotScene, "input.invalid-value":
			ds[i].Next = []string{shellCommand("wa-cli", "status", "--dir", dir, "--json"), help}
		}
	}
}
