package cli

import (
	"fmt"
	"strings"

	"github.com/kw12121212/writing-anything/internal/authorreview"
	"github.com/kw12121212/writing-anything/internal/work"
	"github.com/kw12121212/writing-anything/internal/workspace"
)

// pendingAuthorReviewsHelp 是 review show 与 next 输出 pendingAuthorReviews 的说明。
const pendingAuthorReviewsHelp = "待作者复核（Pending Author Review）数组：strict 关卡下经 cli 渠道写入、尚未由作者在阅读页（Reading View）复核的" +
	"验收、钦定设定修改与卷末检查点确认，每项含 key、kind（acceptance、canon-decision 或 volume-checkpoint）、work、ref、path、" +
	"confirmation、confirmedAt 与 command（打开该作品阅读页的命令，作者在页面上批量复核）；standard 与 fast 关卡为空数组"

// channelView 返回确认记录的确认渠道（Confirmation Channel）输出：未确认为 nil；已确认而缺失渠道的旧记录为 cli。
func channelView(confirmed bool, c *string) *string {
	if !confirmed {
		return nil
	}
	v := workspace.ChannelOr(c)
	return &v
}

// pendingReviewView 是一项待作者复核的确认的输出形态。
type pendingReviewView struct {
	authorreview.Item
	Command string `json:"command"`
}

// previewServeCommand 返回打开作品阅读页（作者在上面确认与复核）的命令。
func previewServeCommand(dir, workID string) string {
	return shellCommand("wa-cli", "preview", workID, "--serve", "--dir", dir, "--json")
}

// pendingReviews 返回 works 中待作者复核的确认：只在 strict 关卡下计算，其他关卡强度为空数组。复核记录无法加载时
// 其问题只报告一次。
func pendingReviews(dir string, works []*work.Work) ([]pendingReviewView, []workspace.Problem) {
	out := []pendingReviewView{}
	if !authorreview.Strict(dir) {
		return out, nil
	}
	var problems []workspace.Problem
	for _, w := range works {
		items, ps := authorreview.Pending(dir, w)
		if problems == nil {
			problems = ps
		}
		for _, it := range items {
			out = append(out, pendingReviewView{Item: it, Command: previewServeCommand(dir, w.ID)})
		}
	}
	return out, problems
}

// pendingReviewsText 返回待作者复核清单的人读文本；清单为空时为空串。
func pendingReviewsText(items []pendingReviewView) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "待作者复核（Pending Author Review）：%d 项经命令行渠道写入的确认，组稿前须由作者在阅读页（Reading View）复核\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "  %s\n", authorreview.Describe(it.Item))
	}
	seen := map[string]bool{}
	for _, it := range items {
		if !seen[it.Command] {
			seen[it.Command] = true
			fmt.Fprintf(&b, "  请作者打开阅读页复核：%s\n", it.Command)
		}
	}
	return b.String()
}
