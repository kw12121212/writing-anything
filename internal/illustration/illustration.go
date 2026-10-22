// Package illustration 管理单元（Unit）的配图登记（Illustration Registry）：单元目录下的 illustrations.yaml 记录每张配图
// （Illustration）与封面图（Cover Image），图片文件放在单元目录的 illustrations/ 下。本包只依赖单元目录与正文字节，不依赖
// 作品与正文包：论断（Claim）与素材（Source）的存在性由调用方传入，只返回结构化的问题，由命令层转换为诊断（Diagnostic）。
// wa-cli 不生成、不处理图片：只按文件头识别 PNG 与 JPEG、读取宽高并原样保存。
package illustration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 的文件头解析，只用于读取宽高
	_ "image/png"  // 注册 PNG 的文件头解析，只用于读取宽高
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kw12121212/writing-anything/internal/catalog"
	"github.com/kw12121212/writing-anything/internal/mdscan"
	"github.com/kw12121212/writing-anything/internal/schema"
	"github.com/kw12121212/writing-anything/internal/workspace"
	"go.yaml.in/yaml/v3"
)

// 配图的错误码（Error Code）。已发布的错误码不得改名或复用。
const (
	CodeUnsupportedFormat     = "illustration.unsupported-format"
	CodeTooLarge              = "illustration.too-large"
	CodeNotFound              = "illustration.not-found"
	CodeCoverNotRoot          = "illustration.cover-not-root"
	CodeCoverExists           = "illustration.cover-exists"
	CodeCoverReferenced       = "illustration.cover-referenced"
	CodeMissingProvenance     = "illustration.missing-provenance"
	CodeUnknownClaim          = "illustration.unknown-claim"
	CodeUnknownSource         = "illustration.unknown-source"
	CodeFileMismatch          = "illustration.file-mismatch"
	CodeFileMissing           = "illustration.file-missing"
	CodeHashMismatch          = "illustration.hash-mismatch"
	CodeUnregisteredReference = "illustration.unregistered-reference"
	CodeUnreferenced          = "illustration.unreferenced"
	CodeOrphanFile            = "illustration.orphan-file"
	CodeAnchoredValue         = "illustration.anchored-value"
)

// 文件名、目录名与取值，与 illustrations.schema.json 一致。
const (
	FileName = "illustrations.yaml"
	DirName  = "illustrations"
	// Prefix 是正文中本地配图引用的地址前缀（区分大小写）。
	Prefix = DirName + "/"
	// MaxBytes 是单张图片的大小上限（10 MiB）。
	MaxBytes = 10 << 20

	RoleBody  = "body"
	RoleCover = "cover"

	ContentMood        = "mood"
	ContentInfographic = "infographic"

	OriginAgent  = "agent"
	OriginAuthor = "author"

	FormatPNG  = "png"
	FormatJPEG = "jpeg"

	maxAlt  = 200
	maxTool = 100
)

var (
	Roles    = []string{RoleBody, RoleCover}
	Contents = []string{ContentMood, ContentInfographic}
	Origins  = []string{OriginAgent, OriginAuthor}
)

// Item 是一张已登记的配图。
type Item struct {
	ID      string   `json:"id"`
	Role    string   `json:"role"`
	File    string   `json:"file"`
	Format  string   `json:"format"`
	SHA256  string   `json:"sha256"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Alt     string   `json:"alt"`
	Title   *string  `json:"title"`
	Content string   `json:"content"`
	Origin  string   `json:"origin"`
	Tool    *string  `json:"tool"`
	Style   *string  `json:"style"`
	Claims  []string `json:"claims"`
	Sources []string `json:"sources"`
	AddedAt string   `json:"added_at"`
}

type registryFile struct {
	Illustrations []Item `json:"illustrations"`
}

// Path 返回单元目录 dir 下 illustrations.yaml 的路径。
func Path(dir string) string { return dir + "/" + FileName }

// ImagePath 返回登记项 it 的图片在工作区中的路径（相对项目目录）。
func ImagePath(dir string, it Item) string { return dir + "/" + it.File }

// Ext 返回格式对应的文件扩展名（不含点）。
func Ext(format string) string {
	if format == FormatPNG {
		return "png"
	}
	return "jpg"
}

// MIME 返回格式对应的媒体类型。
func MIME(format string) string {
	if format == FormatPNG {
		return "image/png"
	}
	return "image/jpeg"
}

func problem(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityError, Path: path, Field: field, Message: msg}
}

func warning(code, path, field, msg string) workspace.Problem {
	return workspace.Problem{Code: code, Severity: workspace.SeverityWarning, Path: path, Field: field, Message: msg}
}

func abs(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// exists 报告 rel 处是否有文件系统条目（不跟随最后一级的符号链接）。
func exists(root, rel string) bool {
	_, err := os.Lstat(abs(root, rel))
	return err == nil
}

// load 读取并按 Schema 校验 illustrations.yaml；不存在时三个返回值都为 nil。途经符号链接或不是普通文件时报告
// artifact.not-found（按不存在处理，不读取项目目录之外的内容）。
func load(root, dir string) (*yaml.Node, *registryFile, []workspace.Problem) {
	rel := Path(dir)
	if _, ok, _ := workspace.ReadRegular(root, rel); !ok {
		if exists(root, rel) {
			return nil, nil, []workspace.Problem{problem(schema.CodeNotFound, rel, "", FileName+" 途经符号链接或不是普通文件，按不存在处理；请把它换成单元目录中的普通文件")}
		}
		return nil, nil, nil
	}
	doc, ps := workspace.Read(root, rel)
	if doc == nil {
		return nil, nil, ps
	}
	if ps := schema.Named("illustrations").ValidateNode(doc); len(ps) > 0 {
		return nil, nil, workspace.FromSchema(rel, ps)
	}
	var f registryFile
	catalog.Decode(doc, &f)
	for i := range f.Illustrations {
		normalize(&f.Illustrations[i])
	}
	return doc, &f, nil
}

func normalize(it *Item) {
	if it.Claims == nil {
		it.Claims = []string{}
	}
	if it.Sources == nil {
		it.Sources = []string{}
	}
}

// Load 读取并按 Schema 校验单元目录 dir 下的配图登记；不存在时返回 nil 与 nil。只做 Schema 校验，领域检查见 Check。
func Load(root, dir string) ([]Item, bool, []workspace.Problem) {
	_, f, ps := load(root, dir)
	if f == nil {
		return nil, len(ps) > 0 || exists(root, Path(dir)), ps
	}
	return f.Illustrations, true, nil
}

// Ref 是正文中的一个本地配图引用。
type Ref struct {
	mdscan.Image
}

// Refs 返回正文中地址以 illustrations/ 开头的图片引用（围栏代码块、行内代码与已闭合的 HTML 注释之外），按出现顺序。
func Refs(content []byte) []Ref {
	var out []Ref
	for _, im := range mdscan.Images(string(content)) {
		if strings.HasPrefix(im.Src, Prefix) {
			out = append(out, Ref{im})
		}
	}
	return out
}

// Context 是领域检查的输入：单元目录、是否为作品根单元、当前正文（没有时为 nil），以及该单元论断与已登记素材的存在性。
type Context struct {
	Root   string
	Dir    string
	IsRoot bool
	Draft  []byte
	// Claim 报告 id 是否为该单元 claims.yaml 中的论断；Source 报告 id 是否为已登记素材。
	Claim  func(id string) bool
	Source func(id string) bool
}

// Issue 是领域检查发现的一个问题；Item 为相关登记项的下标，与登记项无关时为 -1。
type Issue struct {
	workspace.Problem
	Item int
}

// Checked 是领域检查的结果。
type Checked struct {
	Items      []Item
	Exists     bool // 单元目录下有 illustrations.yaml（含不通过校验的）
	Referenced map[string]bool
	Issues     []Issue
}

// Problems 返回全部问题。
func (c *Checked) Problems() []workspace.Problem {
	out := make([]workspace.Problem, 0, len(c.Issues))
	for _, is := range c.Issues {
		out = append(out, is.Problem)
	}
	return out
}

// Errors 返回 error 级问题（配图阻塞项）。
func (c *Checked) Errors() []workspace.Problem {
	var out []workspace.Problem
	for _, is := range c.Issues {
		if is.Severity == workspace.SeverityError {
			out = append(out, is.Problem)
		}
	}
	return out
}

// Codes 返回登记项 i 的问题错误码（按出现顺序去重），没有时为空数组。
func (c *Checked) Codes(i int) []string {
	out := []string{}
	for _, is := range c.Issues {
		if is.Item == i && !slices.Contains(out, is.Code) {
			out = append(out, is.Code)
		}
	}
	return out
}

// Active 报告单元是否需要做配图检查：有 illustrations.yaml，或正文中有本地配图引用。
func Active(root, dir string, draft []byte) bool {
	return exists(root, Path(dir)) || len(Refs(draft)) > 0
}

// Check 对单元做配图的领域检查（illustrations 主规格的 illustration-validation）：Schema、id 唯一、file 与 format 一致、
// 图片存在且哈希一致、论断与素材存在、信息图有出处、封面位置与数量，以及正文中的未登记引用、封面引用与未引用的配图、
// illustrations/ 中多余的文件。只读。
func Check(ctx Context) *Checked {
	rel := Path(ctx.Dir)
	out := &Checked{Referenced: map[string]bool{}, Exists: exists(ctx.Root, rel)}
	add := func(i int, p workspace.Problem) { out.Issues = append(out.Issues, Issue{p, i}) }
	_, f, ps := load(ctx.Root, ctx.Dir)
	for _, p := range ps {
		add(-1, p)
	}
	if f != nil {
		out.Items = f.Illustrations
	}
	files := map[string]int{}
	covers := 0
	seen := map[string]bool{}
	for i, it := range out.Items {
		field := fmt.Sprintf("illustrations[%d]", i)
		if seen[it.ID] {
			add(i, problem(catalog.CodeDuplicateID, rel, field+".id", "配图 id "+it.ID+" 重复"))
		}
		seen[it.ID] = true
		files[it.File] = i
		if want := Prefix + it.ID + "." + Ext(it.Format); it.File != want {
			add(i, problem(CodeFileMismatch, rel, field+".file", fmt.Sprintf("配图 %s 的 file 应为 %s（与 id 和 format 一致）", it.ID, want)))
		} else {
			img := ImagePath(ctx.Dir, it)
			sum, ok := fileSHA(ctx.Root, img)
			switch {
			case !ok:
				add(i, problem(CodeFileMissing, img, "", fmt.Sprintf("配图 %s 的图片 %s 不存在、途经符号链接或不是普通文件；请用 wa-cli illustration add --replace 重新登记", it.ID, img)))
			case sum != it.SHA256:
				add(i, problem(CodeHashMismatch, img, "", fmt.Sprintf("配图 %s 的图片 %s 在登记之后被改动，与登记的 SHA-256 不一致；请用 wa-cli illustration add --replace 重新登记", it.ID, img)))
			}
		}
		for j, id := range it.Claims {
			if ctx.Claim == nil || !ctx.Claim(id) {
				add(i, problem(CodeUnknownClaim, rel, fmt.Sprintf("%s.claims[%d]", field, j), fmt.Sprintf("配图 %s 关联的论断（Claim）%s 不在该单元的 claims.yaml 中", it.ID, id)))
			}
		}
		for j, id := range it.Sources {
			if ctx.Source == nil || !ctx.Source(id) {
				add(i, problem(CodeUnknownSource, rel, fmt.Sprintf("%s.sources[%d]", field, j), fmt.Sprintf("配图 %s 关联的素材（Source）%s 未登记", it.ID, id)))
			}
		}
		if it.Content == ContentInfographic && len(it.Claims) == 0 && len(it.Sources) == 0 {
			add(i, problem(CodeMissingProvenance, rel, field+".content", fmt.Sprintf("信息图（Infographic）%s 没有关联任何论断或素材；信息图中的数据与事实必须有出处", it.ID)))
		}
		if it.Role == RoleCover {
			covers++
			switch {
			case !ctx.IsRoot:
				add(i, problem(CodeCoverNotRoot, rel, field+".role", "封面图（Cover Image）只能登记在作品的根单元"))
			case covers > 1:
				add(i, problem(CodeCoverExists, rel, field+".role", "作品只能有一张封面图（Cover Image）；请删除多余的封面"))
			}
		}
	}
	draftRel := ctx.Dir + "/draft.md"
	for _, r := range Refs(ctx.Draft) {
		i, ok := files[r.Src]
		switch {
		case !ok:
			add(-1, problem(CodeUnregisteredReference, draftRel, "", fmt.Sprintf("正文第 %d 行引用的 %s 不是已登记的配图；请用 wa-cli illustration add 登记，或用 wa-cli draft write 改写引用", r.Line, r.Src)))
		case out.Items[i].Role == RoleCover:
			add(i, warning(CodeCoverReferenced, draftRel, "", fmt.Sprintf("正文第 %d 行引用了封面图（Cover Image）%s；封面不在正文中显示，导出时放入元数据与配图清单", r.Line, r.Src)))
		default:
			out.Referenced[out.Items[i].ID] = true
		}
	}
	for i, it := range out.Items {
		if it.Role == RoleBody && !out.Referenced[it.ID] {
			add(i, warning(CodeUnreferenced, rel, fmt.Sprintf("illustrations[%d].id", i), fmt.Sprintf("配图 %s 已登记，但正文没有引用它；请用 wa-cli draft write 插入引用，或用 wa-cli illustration remove 删除", it.ID)))
		}
	}
	for _, name := range dirEntries(ctx.Root, ctx.Dir+"/"+DirName) {
		if _, ok := files[Prefix+name]; !ok {
			add(-1, warning(CodeOrphanFile, ctx.Dir+"/"+Prefix+name, "", Prefix+name+" 没有对应的配图登记"))
		}
	}
	return out
}

// dirEntries 返回真实目录 rel 中不以 . 开头的条目名，按名称排序；目录不存在或途经符号链接时为空。
func dirEntries(root, rel string) []string {
	if blockedDir(root, rel) {
		return nil
	}
	info, err := os.Lstat(abs(root, rel))
	if err != nil || !info.IsDir() {
		return nil
	}
	es, err := os.ReadDir(abs(root, rel))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// blockedDir 报告目录 rel 在工作区目录之下途经的某一级（含它自己）存在但不是真实目录（如符号链接）；不存在的不算受阻。
func blockedDir(root, rel string) bool {
	segs := strings.Split(rel, "/")
	for i := 2; i <= len(segs); i++ {
		info, err := os.Lstat(abs(root, strings.Join(segs[:i], "/")))
		if err != nil {
			return false
		}
		if !info.IsDir() {
			return true
		}
	}
	return false
}

// fileSHA 返回工作区普通文件 rel 原始字节的 SHA-256；不存在、途经符号链接或不是普通文件时 ok 为 false。
func fileSHA(root, rel string) (string, bool) {
	f, ok, _ := workspace.OpenRegular(root, rel)
	if !ok {
		return "", false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// ReadImage 读取登记项 it 的图片字节；只有图片存在、是普通文件、不超过大小上限且 SHA-256 与登记一致时 ok 为 true。
func ReadImage(root, dir string, it Item) ([]byte, bool) {
	rel := ImagePath(dir, it)
	if it.File != Prefix+it.ID+"."+Ext(it.Format) {
		return nil, false
	}
	f, ok, _ := workspace.OpenRegular(root, rel)
	if !ok {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil || len(data) > MaxBytes {
		return nil, false
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != it.SHA256 {
		return nil, false
	}
	return data, true
}

// Digest 返回单元目录 dir 的配图摘要（illustrations_sha256）：没有 illustrations.yaml 或 illustrations 为空时为 nil。
// 摘要由按 id 排序的每个登记项的 id、role、format、图片实际字节的 SHA-256（不可读时为 missing）、alt、title、content、
// origin、tool、style、claims 与 sources 决定；added_at、宽高、条目顺序与 YAML 格式不影响摘要。illustrations.yaml 不通过
// 校验时，摘要由其原始字节决定（与任何有效登记的摘要都不同），验收因此视为失效。
func Digest(root, dir string) *string {
	rel := Path(dir)
	if !exists(root, rel) {
		return nil
	}
	_, f, ps := load(root, dir)
	h := sha256.New()
	if f == nil || len(ps) > 0 {
		raw, _, _ := workspace.ReadRegular(root, rel)
		h.Write([]byte("invalid\n"))
		h.Write(raw)
		s := hex.EncodeToString(h.Sum(nil))
		return &s
	}
	if len(f.Illustrations) == 0 {
		return nil
	}
	items := slices.Clone(f.Illustrations)
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for _, it := range items {
		sum, ok := fileSHA(root, ImagePath(dir, it))
		if !ok {
			sum = "missing"
		}
		line, _ := json.Marshal([]any{it.ID, it.Role, it.Format, sum, it.Alt, it.Title, it.Content, it.Origin, it.Tool, it.Style, it.Claims, it.Sources})
		h.Write(line)
		h.Write([]byte("\n"))
	}
	s := hex.EncodeToString(h.Sum(nil))
	return &s
}

// Detect 按文件开头的字节识别 PNG 或 JPEG，并从文件头读取宽高；不是这两种格式或读不出宽高时返回空格式。
func Detect(data []byte) (format string, width, height int) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		format = FormatPNG
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		format = FormatJPEG
	default:
		return "", 0, 0
	}
	cfg, name, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || name != format || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, 0
	}
	return format, cfg.Width, cfg.Height
}

// AddInput 是 illustration add 的参数。
type AddInput struct {
	ID      string
	Data    []byte
	Alt     string
	Title   *string
	Role    string
	Content string
	Origin  string
	Tool    *string
	Style   *string
	Claims  []string
	Sources []string
	Replace bool
	IsRoot  bool
	// Claim 与 Source 报告本次关联的论断与素材是否存在（只检查本次参数）。
	Claim  func(id string) bool
	Source func(id string) bool
}

// AddResult 是 illustration add 的结果。
type AddResult struct {
	Item         Item
	PreviousPath string // 替换时格式变化导致图片路径改变，此为原路径；否则为空
}

// idPattern 与 common.schema.json 中的 id 一致。
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

func validID(id string) bool { return idPattern.MatchString(id) }

func line(v string) bool { return !strings.ContainsAny(v, "\r\n") }

// params 检查参数，返回第一个问题；字段为参数名，不填路径。
func (in AddInput) params() *workspace.Problem {
	bad := func(field, msg string) *workspace.Problem {
		p := problem(schema.CodeSchemaViolation, "", field, msg)
		return &p
	}
	switch {
	case !validID(in.ID):
		return bad("id", "配图 id "+in.ID+" 格式不正确：只能由小写字母、数字与连字符组成，以字母开头")
	case strings.TrimSpace(in.Alt) == "" || !line(in.Alt) || utf8.RuneCountInString(in.Alt) > maxAlt:
		return bad("alt", "替代文字（--alt）必须是一行非空文字，不超过 200 个字符")
	case in.Title != nil && (!line(*in.Title) || utf8.RuneCountInString(*in.Title) > maxAlt):
		return bad("title", "标题（--title）必须是一行文字，不超过 200 个字符")
	case in.Tool != nil && (!line(*in.Tool) || utf8.RuneCountInString(*in.Tool) > maxTool):
		return bad("tool", "--tool 必须是一行文字，不超过 100 个字符")
	case in.Style != nil && (!line(*in.Style) || utf8.RuneCountInString(*in.Style) > maxTool):
		return bad("style", "--style 必须是一行文字，不超过 100 个字符")
	case !slices.Contains(Roles, in.Role):
		return bad("role", "--role 只能是 body 或 cover")
	case !slices.Contains(Contents, in.Content):
		return bad("content", "--content 只能是 mood（氛围图）或 infographic（信息图）")
	case in.Origin == "":
		return bad("origin", "必须用 --origin 给出出处：Agent 渲染的图为 agent，作者亲自提供的图为 author")
	case !slices.Contains(Origins, in.Origin):
		return bad("origin", "--origin 只能是 agent 或 author")
	}
	for i, id := range in.Claims {
		if !validID(id) {
			return bad(fmt.Sprintf("claims[%d]", i), "论断 id "+id+" 格式不正确")
		}
	}
	for i, id := range in.Sources {
		if !validID(id) {
			return bad(fmt.Sprintf("sources[%d]", i), "素材 id "+id+" 格式不正确")
		}
	}
	return nil
}

func one(p workspace.Problem) []workspace.Problem { return []workspace.Problem{p} }

// Add 登记或替换（in.Replace）单元目录 dir 中的配图：检查参数、图片格式与大小、封面与出处，全部通过后先写图片再写
// illustrations.yaml（保留其他内容与注释）。ensureDir 在写入前创建单元目录并返回新建的路径。返回结果、变更的路径与问题；
// 出错时不修改文件。
func Add(root, dir string, in AddInput, ensureDir func() ([]string, []workspace.Problem), now time.Time) (*AddResult, []string, []workspace.Problem) {
	rel := Path(dir)
	if p := in.params(); p != nil {
		return nil, nil, one(*p)
	}
	if len(in.Data) > MaxBytes {
		return nil, nil, one(problem(CodeTooLarge, "", "file", fmt.Sprintf("图片有 %d 字节，超过 10 MiB 的上限；wa-cli 不压缩图片，请先缩小后再登记", len(in.Data))))
	}
	format, width, height := Detect(in.Data)
	if format == "" {
		return nil, nil, one(problem(CodeUnsupportedFormat, "", "file", "只能登记 PNG 或 JPEG 图片（按文件开头的字节识别，与扩展名无关），且能从文件头读出宽高"))
	}
	if in.Role == RoleCover && !in.IsRoot {
		return nil, nil, one(problem(CodeCoverNotRoot, "", "role", "封面图（Cover Image）只能登记在作品的根单元"))
	}
	if in.Content == ContentInfographic && len(in.Claims) == 0 && len(in.Sources) == 0 {
		return nil, nil, one(problem(CodeMissingProvenance, "", "content", "信息图（Infographic）必须用 --claims 或 --sources 关联至少一个论断或素材；请先用 wa-cli claim add 或 wa-cli source add 登记再关联"))
	}
	for i, id := range in.Claims {
		if in.Claim == nil || !in.Claim(id) {
			return nil, nil, one(problem(CodeUnknownClaim, "", fmt.Sprintf("claims[%d]", i), "论断（Claim）"+id+" 不在该单元的 claims.yaml 中"))
		}
	}
	for i, id := range in.Sources {
		if in.Source == nil || !in.Source(id) {
			return nil, nil, one(problem(CodeUnknownSource, "", fmt.Sprintf("sources[%d]", i), "素材（Source）"+id+" 未登记"))
		}
	}
	doc, f, ps := load(root, dir)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	at := -1
	if f != nil {
		for i, it := range f.Illustrations {
			if it.ID == in.ID {
				at = i
			}
		}
	}
	switch {
	case in.Replace && at < 0:
		return nil, nil, one(problem(CodeNotFound, "", "id", "单元中没有配图 "+in.ID+"；--replace 只能替换已登记的配图"))
	case !in.Replace && at >= 0:
		return nil, nil, one(problem(catalog.CodeDuplicateID, rel, "id", "单元中已有配图 "+in.ID+"；要替换它请加 --replace"))
	case in.Replace && f.Illustrations[at].Role != in.Role:
		return nil, nil, one(problem(schema.CodeSchemaViolation, "", "role", "--replace 不能改变配图的用途（role）；需要改变用途时先用 wa-cli illustration remove 删除再登记"))
	}
	if in.Role == RoleCover && f != nil {
		for _, it := range f.Illustrations {
			if it.Role == RoleCover && it.ID != in.ID {
				return nil, nil, one(problem(CodeCoverExists, "", "role", "作品已有封面图（Cover Image）"+it.ID+"；要更换封面请用 --id "+it.ID+" --replace，或先删除现有封面"))
			}
		}
	}
	sum := sha256.Sum256(in.Data)
	item := Item{ID: in.ID, Role: in.Role, File: Prefix + in.ID + "." + Ext(format), Format: format, SHA256: hex.EncodeToString(sum[:]),
		Width: width, Height: height, Alt: in.Alt, Title: in.Title, Content: in.Content, Origin: in.Origin, Tool: in.Tool, Style: in.Style,
		Claims: nonNil(in.Claims), Sources: nonNil(in.Sources), AddedAt: now.UTC().Format("2006-01-02T15:04:05Z")}
	res := &AddResult{Item: item}
	var old *Item
	if at >= 0 {
		old = &f.Illustrations[at]
		if old.File != item.File {
			res.PreviousPath = ImagePath(dir, *old)
		}
	}
	if doc == nil {
		doc = &yaml.Node{Kind: yaml.DocumentNode, HeadComment: "配图登记（Illustration Registry）；请用 wa-cli illustration add 写入。", Content: []*yaml.Node{{Kind: yaml.MappingNode, Content: []*yaml.Node{
			workspace.Str("schema_version"), workspace.Int(1),
			workspace.Str("illustrations"), {Kind: yaml.SequenceNode, Tag: "!!seq"},
		}}}}
	}
	seq := itemsNode(doc)
	if seq == nil {
		return nil, nil, one(problem(CodeAnchoredValue, rel, "illustrations", "illustrations 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只修改一项；请先去掉锚点、别名与合并键"))
	}
	node := itemNode(item)
	if at >= 0 {
		if workspace.HasAnchor(seq.Content[at]) {
			return nil, nil, one(problem(CodeAnchoredValue, rel, fmt.Sprintf("illustrations[%d]", at), "该配图登记中有 YAML 锚点、别名或合并键，替换会改变其他取值；请先去掉锚点、别名与合并键"))
		}
		node.HeadComment, node.LineComment, node.FootComment = seq.Content[at].HeadComment, seq.Content[at].LineComment, seq.Content[at].FootComment
		seq.Content[at] = node
	} else {
		workspace.BlockSequence(doc.Content[0], "illustrations")
		seq.Content = append(seq.Content, node)
	}
	data, ps := encodeChecked(rel, doc)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	imgDir := dir + "/" + DirName
	imgRel := dir + "/" + item.File
	if blockedDir(root, imgDir) {
		return nil, nil, one(problem(workspace.CodeWriteFailed, imgDir, "", imgDir+" 途经符号链接或不是真实目录，wa-cli 不跟随它写入"))
	}
	for _, p := range []string{imgRel, rel} {
		if workspace.Blocked(root, p) {
			return nil, nil, one(problem(workspace.CodeWriteFailed, p, "", p+" 途经符号链接或不是真实目录、普通文件，wa-cli 不跟随它写入"))
		}
	}
	if res.PreviousPath != "" && workspace.Blocked(root, res.PreviousPath) {
		return nil, nil, one(problem(workspace.CodeWriteFailed, res.PreviousPath, "", res.PreviousPath+" 途经符号链接或不是普通文件，wa-cli 不删除它"))
	}
	changed, ps := ensureDir()
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if !exists(root, imgDir) {
		changed = append(changed, imgDir)
	}
	if err := workspace.EnsureDir(root, imgDir); err != nil {
		return nil, nil, one(problem(workspace.CodeWriteFailed, imgDir, "", "创建配图目录失败："+err.Error()))
	}
	if err := workspace.WriteFile(root, imgRel, in.Data); err != nil {
		return nil, nil, one(problem(workspace.CodeWriteFailed, imgRel, "", "写入图片失败："+err.Error()))
	}
	changed = append(changed, imgRel)
	if res.PreviousPath != "" {
		if err := os.Remove(abs(root, res.PreviousPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, one(problem(workspace.CodeWriteFailed, res.PreviousPath, "", "删除旧图片失败："+err.Error()))
		} else if err == nil {
			changed = append(changed, res.PreviousPath)
		}
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, one(problem(workspace.CodeWriteFailed, rel, "", "写入配图登记失败："+err.Error()))
	}
	return res, append(changed, rel), nil
}

func nonNil(vs []string) []string {
	if vs == nil {
		return []string{}
	}
	return vs
}

// itemOrder 是登记项中字段的固定顺序。
var itemOrder = []string{"id", "role", "file", "format", "sha256", "width", "height", "alt", "title", "content", "origin", "tool", "style", "claims", "sources", "added_at"}

func itemNode(it Item) *yaml.Node {
	opt := func(v *string) *yaml.Node {
		if v == nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
		}
		return workspace.Str(*v)
	}
	flow := func(vs []string) *yaml.Node {
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, v := range vs {
			n.Content = append(n.Content, workspace.Str(v))
		}
		return n
	}
	vals := map[string]*yaml.Node{
		"id": workspace.Str(it.ID), "role": workspace.Str(it.Role), "file": workspace.Str(it.File), "format": workspace.Str(it.Format),
		"sha256": workspace.Str(it.SHA256), "width": workspace.Int(it.Width), "height": workspace.Int(it.Height), "alt": workspace.Str(it.Alt),
		"title": opt(it.Title), "content": workspace.Str(it.Content), "origin": workspace.Str(it.Origin), "tool": opt(it.Tool), "style": opt(it.Style),
		"claims": flow(it.Claims), "sources": flow(it.Sources), "added_at": workspace.Str(it.AddedAt),
	}
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, k := range itemOrder {
		n.Content = append(n.Content, workspace.Str(k), vals[k])
	}
	return n
}

// itemsNode 返回文档中 illustrations 序列节点；途经锚点、别名或合并键时返回 nil（Schema 保证它存在）。
func itemsNode(doc *yaml.Node) *yaml.Node {
	m := doc.Content[0]
	if !workspace.Plain(m) || workspace.HasMerge(m) {
		return nil
	}
	v, ok := workspace.Lookup(m, "illustrations")
	if v == nil || !ok {
		return nil
	}
	return v
}

// encodeChecked 编码文档并做写前复查：重新解析后 Schema 校验通过才返回字节。
func encodeChecked(rel string, doc *yaml.Node) ([]byte, []workspace.Problem) {
	data, err := workspace.Encode(doc)
	if err != nil {
		return nil, one(problem(workspace.CodeWriteFailed, rel, "", "生成配图登记失败："+err.Error()))
	}
	written, p := schema.Parse(data)
	if p != nil {
		p.Line = 0
		return nil, workspace.FromSchema(rel, []schema.Problem{*p})
	}
	if ps := schema.Named("illustrations").ValidateNode(written); len(ps) > 0 {
		return nil, workspace.FromSchema(rel, ps)
	}
	return data, nil
}

// Remove 删除单元目录 dir 中配图 id 的登记项与图片文件，保留其他内容与注释。返回被删除的登记项与变更的路径；
// 出错时不修改文件。
func Remove(root, dir, id string) (*Item, []string, []workspace.Problem) {
	rel := Path(dir)
	doc, f, ps := load(root, dir)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	notFound := one(problem(CodeNotFound, "", "id", "单元中没有配图 "+id))
	if f == nil {
		return nil, nil, notFound
	}
	seq := itemsNode(doc)
	if seq == nil {
		return nil, nil, one(problem(CodeAnchoredValue, rel, "illustrations", "illustrations 经由 YAML 锚点、别名或合并键得到，wa-cli 无法只删除一项；请先去掉锚点、别名与合并键"))
	}
	at := workspace.IndexByID(seq, id)
	if at < 0 {
		return nil, nil, notFound
	}
	if workspace.HasAnchor(seq.Content[at]) {
		return nil, nil, one(problem(CodeAnchoredValue, rel, fmt.Sprintf("illustrations[%d]", at), "该配图登记中有 YAML 锚点、别名或合并键，删除会改变其他取值；请先去掉锚点、别名与合并键"))
	}
	var it Item
	for _, x := range f.Illustrations {
		if x.ID == id {
			it = x
		}
	}
	img := ImagePath(dir, it)
	if workspace.Blocked(root, img) || workspace.Blocked(root, rel) {
		return nil, nil, one(problem(workspace.CodeWriteFailed, img, "", img+" 途经符号链接或不是普通文件，wa-cli 不删除它"))
	}
	seq.Content = slices.Delete(seq.Content, at, at+1)
	if len(seq.Content) == 0 {
		seq.Style = yaml.FlowStyle
	}
	data, ps := encodeChecked(rel, doc)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	if err := workspace.WriteFile(root, rel, data); err != nil {
		return nil, nil, one(problem(workspace.CodeWriteFailed, rel, "", "写入配图登记失败："+err.Error()))
	}
	changed := []string{rel}
	if it.File == Prefix+it.ID+"."+Ext(it.Format) {
		if err := os.Remove(abs(root, img)); err == nil {
			changed = append(changed, img)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, one(problem(workspace.CodeWriteFailed, img, "", "删除图片失败："+err.Error()))
		}
	}
	return &it, changed, nil
}

// Referenced 报告正文 content 是否引用了登记项 it（地址与 file 完全相同）。
func Referenced(content []byte, it Item) bool {
	for _, r := range Refs(content) {
		if r.Src == it.File {
			return true
		}
	}
	return false
}

// Markdown 返回正文配图可直接插入正文的引用文字；封面返回空。
func Markdown(it Item) string {
	if it.Role != RoleBody {
		return ""
	}
	alt := strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(it.Alt)
	return "![" + alt + "](" + it.File + ")"
}
