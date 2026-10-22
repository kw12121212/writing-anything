package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/draft"
	"github.com/kw12121212/writing-anything/internal/illustration"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/source"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

var illustrationAddCommand = &command{
	name:    "illustration add",
	summary: "把配图（Illustration）或封面图（Cover Image）登记到单元（Unit）的配图登记（Illustration Registry）：原样保存 PNG 或 JPEG 图片并记录用途、替代文字、内容类型与出处",
	args:    []argSpec{{"unit", unitRefHelp, true}},
	flags: []flagSpec{
		dirFlag,
		{"id", flagString, "配图 id（小写字母、数字与连字符）；图片保存为 illustrations/<id>.png 或 illustrations/<id>.jpg"},
		{"file", flagString, "图片文件（PNG 或 JPEG，不超过 10 MiB），相对 --dir 或为绝对路径"},
		{"alt", flagString, "替代文字：一行，不超过 200 个字符"},
		{"origin", flagString, "出处：agent（Agent 渲染或生成的图）或 author（作者亲自提供的图）；必须给出"},
		{"role", flagString, "用途：body（正文配图，默认）或 cover（作品封面图，只能登记在根单元）"},
		{"content", flagString, "内容类型：mood（氛围图（Mood Image），默认）或 infographic（信息图（Infographic），必须关联论断或素材）"},
		{"title", flagString, "标题（图片说明）：一行，不超过 200 个字符"},
		{"tool", flagString, "生成或渲染图片所用的工具名"},
		{"style", flagString, "风格，如 watercolor"},
		{"claims", flagString, "关联的论断（Claim）id，用逗号分隔"},
		{"sources", flagString, "关联的素材（Source）id，用逗号分隔"},
		{"replace", flagBool, "原位替换已登记的同 id 配图（用途不变；格式不变时图片路径不变）"},
	},
	outputKeys: []outputKey{{"illustration", "登记的配图：unit、id、role、path、format、sha256、width、height、content、origin、referenced（正文是否引用）与 markdown（可插入正文的引用，封面为 null），格式变化时另含 previousPath；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runIllustrationAdd,
}

var illustrationListCommand = &command{
	name:       "illustration list",
	summary:    "列出单元（Unit）登记的配图（Illustration）与封面图（Cover Image），并标出每张配图的问题",
	args:       []argSpec{{"unit", unitRefHelp, true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"illustrations", "按登记顺序排列的配图数组，每项含 unit、id、role、path、format、sha256、width、height、alt、title、content、origin、tool、style、claims、sources、addedAt、referenced 与 problems（问题错误码数组）"}},
	exitCodes:  standardExitCodes,
	run:        runIllustrationList,
}

var illustrationRemoveCommand = &command{
	name:       "illustration remove",
	summary:    "删除单元（Unit）中一张配图（Illustration）的登记与图片文件，保留其他内容与注释",
	args:       []argSpec{{"unit", unitRefHelp, true}, {"illustration", "配图 id", true}},
	flags:      []flagSpec{dirFlag},
	outputKeys: []outputKey{{"illustration", "被删除的配图：unit、id、role 与 path；失败时为 null"}},
	exitCodes:  standardExitCodes,
	run:        runIllustrationRemove,
}

type illustrationAddView struct {
	Unit         string  `json:"unit"`
	ID           string  `json:"id"`
	Role         string  `json:"role"`
	Path         string  `json:"path"`
	Format       string  `json:"format"`
	SHA256       string  `json:"sha256"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Content      string  `json:"content"`
	Origin       string  `json:"origin"`
	Referenced   bool    `json:"referenced"`
	Markdown     *string `json:"markdown"`
	PreviousPath string  `json:"previousPath,omitempty"`
}

type illustrationAddResult struct {
	Result
	Illustration *illustrationAddView `json:"illustration"`
}

type illustrationView struct {
	Unit       string   `json:"unit"`
	ID         string   `json:"id"`
	Role       string   `json:"role"`
	Path       string   `json:"path"`
	Format     string   `json:"format"`
	SHA256     string   `json:"sha256"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	Alt        string   `json:"alt"`
	Title      *string  `json:"title"`
	Content    string   `json:"content"`
	Origin     string   `json:"origin"`
	Tool       *string  `json:"tool"`
	Style      *string  `json:"style"`
	Claims     []string `json:"claims"`
	Sources    []string `json:"sources"`
	AddedAt    string   `json:"addedAt"`
	Referenced bool     `json:"referenced"`
	Problems   []string `json:"problems"`
}

type illustrationListResult struct {
	Result
	Illustrations []illustrationView `json:"illustrations"`
}

type illustrationRemoveView struct {
	Unit string `json:"unit"`
	ID   string `json:"id"`
	Role string `json:"role"`
	Path string `json:"path"`
}

type illustrationRemoveResult struct {
	Result
	Illustration *illustrationRemoveView `json:"illustration"`
}

// readImage 读取 --file 指定的图片（相对 dir 解析），省略 --file 时读取标准输入；最多读到大小上限多 1 字节，超限由领域包报告。
// 标准输入（含 MCP 的 input）可以是图片的原始字节，也可以是其 base64 文本（可带 data:image/png;base64, 或
// data:image/jpeg;base64, 前缀）：原始字节不是 PNG 或 JPEG 而能按 base64 解码时使用解码结果。
func readImage(inv *invocation, dir string) ([]byte, []workspace.Problem) {
	given, ok := inv.values["file"]
	if !ok {
		data, err := io.ReadAll(io.LimitReader(inv.stdin, illustration.MaxBytes/3*4+1024))
		if err != nil {
			return nil, []workspace.Problem{{Code: codeInputReadFailed, Severity: workspace.SeverityError, Message: "读取标准输入失败：" + err.Error()}}
		}
		return decodeImageInput(data), nil
	}
	path := given
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		var data []byte
		if data, err = io.ReadAll(io.LimitReader(f, illustration.MaxBytes+1)); err == nil {
			return data, nil
		}
	}
	return nil, []workspace.Problem{{Code: codeInputReadFailed, Severity: workspace.SeverityError, Message: fmt.Sprintf("无法读取图片文件 %s：%v", given, err)}}
}

// decodeImageInput 返回标准输入中的图片字节：已是 PNG 或 JPEG 时原样返回，否则尝试按 base64（可带 data URL 前缀）解码。
func decodeImageInput(data []byte) []byte {
	if f, _, _ := illustration.Detect(data); f != "" {
		return data
	}
	text := strings.TrimSpace(string(data))
	for _, prefix := range []string{"data:image/png;base64,", "data:image/jpeg;base64,"} {
		text = strings.TrimPrefix(text, prefix)
	}
	text = strings.Join(strings.Fields(text), "")
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
		return decoded
	}
	return data
}

// unitClaims 返回单元 u 的论断 id 是否存在的判断；claims.yaml 不通过 Schema 校验时一律不存在。
func unitClaims(dir string, w *work.Work, u *work.Unit) func(string) bool {
	cs, _ := draft.ListClaims(dir, w, u)
	ids := map[string]bool{}
	for _, c := range cs {
		ids[c.ID] = true
	}
	return func(id string) bool { return ids[id] }
}

func sourceExists(dir string) func(string) bool {
	return func(id string) bool { return source.Registered(dir, id) }
}

func flagValue(inv *invocation, name string) *string {
	if v, ok := inv.values[name]; ok {
		return &v
	}
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// illustrationAddArgv 按本次参数重建 illustration add 命令（unit 换为 ref，extra 追加在末尾），供诊断的下一步使用。
func illustrationAddArgv(inv *invocation, ref, id, dir string, extra ...string) string {
	argv := []string{"wa-cli", "illustration", "add", ref, "--id", id}
	for _, name := range []string{"file", "alt", "origin", "role", "content", "title", "tool", "style", "claims", "sources"} {
		if v, ok := inv.values[name]; ok {
			argv = append(argv, "--"+name, v)
		}
	}
	argv = append(argv, extra...)
	return shellCommand(append(argv, "--dir", dir, "--json")...)
}

func runIllustrationAdd(inv *invocation) int {
	dir := projectDir(inv)
	res := &illustrationAddResult{}
	problems := workspace.Require(dir)
	var w *work.Work
	var u *work.Unit
	var coverID string
	if problems == nil {
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			var data []byte
			if data, problems = readImage(inv, dir); problems == nil {
				in := illustration.AddInput{ID: inv.values["id"], Data: data, Alt: inv.values["alt"], Title: flagValue(inv, "title"),
					Role: orDefault(inv.values["role"], illustration.RoleBody), Content: orDefault(inv.values["content"], illustration.ContentMood),
					Origin: inv.values["origin"], Tool: flagValue(inv, "tool"), Style: flagValue(inv, "style"),
					Claims: splitSources(inv.values["claims"]), Sources: splitSources(inv.values["sources"]), Replace: inv.flag("replace"),
					IsRoot: u.Parent == nil, Claim: unitClaims(dir, w, u), Source: sourceExists(dir)}
				unitDir := w.UnitDir(u)
				var r *illustration.AddResult
				var changed []string
				r, changed, problems = illustration.Add(dir, unitDir, in, func() ([]string, []workspace.Problem) { return draft.EnsureUnitDir(dir, w, u) }, now())
				if r != nil {
					content, _, _ := draft.Read(dir, w, u)
					v := &illustrationAddView{Unit: u.Ref, ID: r.Item.ID, Role: r.Item.Role, Path: illustration.ImagePath(unitDir, r.Item),
						Format: r.Item.Format, SHA256: r.Item.SHA256, Width: r.Item.Width, Height: r.Item.Height, Content: r.Item.Content,
						Origin: r.Item.Origin, Referenced: illustration.Referenced(content, r.Item), PreviousPath: r.PreviousPath}
					if md := illustration.Markdown(r.Item); md != "" {
						v.Markdown = &md
					}
					res.Illustration, res.ChangedPaths = v, changed
				}
				if items, _, _ := illustration.Load(dir, unitDir); slices.ContainsFunc(problems, func(p workspace.Problem) bool { return p.Code == illustration.CodeCoverExists }) {
					for _, it := range items {
						if it.Role == illustration.RoleCover {
							coverID = it.ID
						}
					}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		help := "wa-cli help illustration add --json"
		switch {
		case d.Code == catalog.CodeDuplicateID && d.Field == "id":
			res.Diagnostics[i].Next = []string{illustrationAddArgv(inv, inv.args[0], inv.values["id"], dir, "--replace"), help}
		case d.Code == illustration.CodeCoverExists && coverID != "":
			res.Diagnostics[i].Next = []string{illustrationAddArgv(inv, inv.args[0], coverID, dir, "--replace"),
				shellCommand("wa-cli", "illustration", "remove", inv.args[0], coverID, "--dir", dir, "--json")}
		case d.Code == illustration.CodeCoverNotRoot && w != nil:
			res.Diagnostics[i].Next = []string{illustrationAddArgv(inv, w.ID, inv.values["id"], dir), help}
		case d.Code == illustration.CodeNotFound:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "illustration", "list", inv.args[0], "--dir", dir, "--json"), help}
		case d.Code == illustration.CodeMissingProvenance:
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "claim", "list", inv.args[0], "--dir", dir, "--json"), shellCommand("wa-cli", "source", "list", "--dir", dir, "--json")}
		case d.Code == illustration.CodeUnknownClaim && d.Path == "":
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "claim", "list", inv.args[0], "--dir", dir, "--json"), help}
		case d.Code == illustration.CodeUnknownSource && d.Path == "":
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "source", "list", "--dir", dir, "--json"), help}
		case d.Path == "" && (d.Code == schema.CodeSchemaViolation || d.Code == illustration.CodeUnsupportedFormat || d.Code == illustration.CodeTooLarge || d.Code == codeInputReadFailed):
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "illustration", "list", inv.args[0], "--dir", dir, "--json"), help}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		v := res.Illustration
		if v == nil {
			return nil
		}
		verb := "已登记"
		if inv.flag("replace") {
			verb = "已替换"
		}
		text := fmt.Sprintf("%s单元（Unit）%s 的%s %s：%s（%s，%d×%d）\n", verb, v.Unit, map[string]string{"body": "配图（Illustration）", "cover": "封面图（Cover Image）"}[v.Role], v.ID, v.Path, v.Format, v.Width, v.Height)
		if v.PreviousPath != "" {
			text += "图片格式改变，路径由 " + v.PreviousPath + " 改为 " + v.Path + "；请用 wa-cli draft write 更新正文中的引用\n"
		}
		if v.Markdown != nil && !v.Referenced {
			text += "在正文中插入：" + *v.Markdown + "\n"
		}
		_, err := io.WriteString(out, text)
		return err
	})
}

func runIllustrationList(inv *invocation) int {
	dir := projectDir(inv)
	res := &illustrationListResult{Illustrations: []illustrationView{}}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			unitDir := w.UnitDir(u)
			content, _, _ := draft.Read(dir, w, u)
			c := illustration.Check(illustration.Context{Root: dir, Dir: unitDir, IsRoot: u.Parent == nil, Draft: content,
				Claim: unitClaims(dir, w, u), Source: sourceExists(dir)})
			if _, _, ps := illustration.Load(dir, unitDir); len(ps) > 0 {
				problems = ps
			} else {
				for i, it := range c.Items {
					res.Illustrations = append(res.Illustrations, illustrationView{Unit: u.Ref, ID: it.ID, Role: it.Role, Path: illustration.ImagePath(unitDir, it),
						Format: it.Format, SHA256: it.SHA256, Width: it.Width, Height: it.Height, Alt: it.Alt, Title: it.Title, Content: it.Content,
						Origin: it.Origin, Tool: it.Tool, Style: it.Style, Claims: it.Claims, Sources: it.Sources, AddedAt: it.AddedAt,
						Referenced: c.Referenced[it.ID], Problems: c.Codes(i)})
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	return finish(inv, res, func(out io.Writer) error {
		var b strings.Builder
		if len(res.Illustrations) == 0 && !hasError(res.Diagnostics) {
			fmt.Fprintf(&b, "单元（Unit）%s 还没有登记配图（Illustration）\n", inv.args[0])
		}
		for _, v := range res.Illustrations {
			fmt.Fprintf(&b, "%s（%s，%s，%s）%s：%s", v.ID, v.Role, v.Content, v.Origin, v.Path, v.Alt)
			if len(v.Problems) > 0 {
				fmt.Fprintf(&b, "；问题：%s", strings.Join(v.Problems, "、"))
			}
			b.WriteString("\n")
		}
		_, err := io.WriteString(out, b.String())
		return err
	})
}

func runIllustrationRemove(inv *invocation) int {
	dir := projectDir(inv)
	res := &illustrationRemoveResult{}
	problems := workspace.Require(dir)
	if problems == nil {
		var w *work.Work
		var u *work.Unit
		if w, u, problems = work.Resolve(dir, inv.args[0]); u != nil {
			unitDir := w.UnitDir(u)
			var it *illustration.Item
			var changed []string
			if it, changed, problems = illustration.Remove(dir, unitDir, inv.args[1]); it != nil {
				res.Illustration = &illustrationRemoveView{Unit: u.Ref, ID: it.ID, Role: it.Role, Path: illustration.ImagePath(unitDir, *it)}
				res.ChangedPaths = changed
				content, _, _ := draft.Read(dir, w, u)
				for _, r := range illustration.Refs(content) {
					if r.Src == it.File {
						problems = append(problems, workspace.Problem{Code: illustration.CodeUnregisteredReference, Severity: workspace.SeverityWarning,
							Path: unitDir + "/" + draft.FileName, Message: fmt.Sprintf("正文第 %d 行仍引用已删除的配图 %s；请用 wa-cli draft write 去掉该引用", r.Line, r.Src)})
					}
				}
			}
		}
	}
	res.Diagnostics = diagnostics(problems, dir)
	for i, d := range res.Diagnostics {
		if d.Code == illustration.CodeNotFound || d.Code == illustration.CodeUnregisteredReference {
			res.Diagnostics[i].Next = []string{shellCommand("wa-cli", "illustration", "list", inv.args[0], "--dir", dir, "--json")}
		}
	}
	return finish(inv, res, func(out io.Writer) error {
		if res.Illustration == nil {
			return nil
		}
		_, err := fmt.Fprintf(out, "已删除单元（Unit）%s 的配图 %s（%s）\n", res.Illustration.Unit, res.Illustration.ID, res.Illustration.Path)
		return err
	})
}

// checkUnitIllustrations 对单元 u 做配图的领域检查（illustration-validation）；单元没有 illustrations.yaml 且正文没有本地配图
// 引用时返回 nil。
func checkUnitIllustrations(dir string, w *work.Work, u *work.Unit) *illustration.Checked {
	unitDir := w.UnitDir(u)
	content, _, _ := draft.Read(dir, w, u)
	if !illustration.Active(dir, unitDir, content) {
		return nil
	}
	return illustration.Check(illustration.Context{Root: dir, Dir: unitDir, IsRoot: u.Parent == nil, Draft: content,
		Claim: unitClaims(dir, w, u), Source: sourceExists(dir)})
}


// checkIllustrations 校验每个作品的每个单元的配图登记与正文中的本地配图引用，返回已校验的 illustrations.yaml 与问题。
func checkIllustrations(dir string, works []*work.Work) (checked []string, problems []workspace.Problem) {
	for _, w := range works {
		for _, u := range w.Units() {
			c := checkUnitIllustrations(dir, w, u)
			if c == nil {
				continue
			}
			if c.Exists {
				checked = append(checked, illustration.Path(w.UnitDir(u)))
			}
			problems = append(problems, c.Problems()...)
		}
	}
	return checked, problems
}

// illustrationArtifact 对 validate <path> 给出的 illustrations.yaml 做与整体校验相同的检查；作品无法加载时只做 Schema 校验
// （其问题由作品校验报告）。
func illustrationArtifact(dir, rel string) []workspace.Problem {
	w, u, _ := work.Resolve(dir, work.RefOf(rel[:strings.LastIndex(rel, "/")]))
	if w == nil || u == nil {
		return nil
	}
	if c := checkUnitIllustrations(dir, w, u); c != nil {
		return c.Problems()
	}
	return nil
}

// illustrationUnitRef 返回配图问题路径（单元目录下的 draft.md、illustrations.yaml 或 illustrations/ 中的图片）所在单元的引用。
func illustrationUnitRef(path string) string {
	unitDir := path[:strings.LastIndex(path, "/")]
	if strings.HasSuffix(unitDir, "/"+illustration.DirName) {
		unitDir = strings.TrimSuffix(unitDir, "/"+illustration.DirName)
	}
	return workspace.UnitRef(unitDir)
}
