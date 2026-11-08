package mygonative

import (
	"fmt"
	"strings"

	"virdeck/core/service"
)

// action identifies an operation the user can run on one or more instances.
type action int

const (
	actStart action = iota
	actStop
	actClone
	actScreen
	actInstallAPK
	actShell
	actProxy
	actDelete
	actCreate
)

func (a action) label() string {
	switch a {
	case actStart:
		return "启动"
	case actStop:
		return "停止"
	case actClone:
		return "克隆"
	case actScreen:
		return "投屏"
	case actInstallAPK:
		return "安装 APK"
	case actShell:
		return "批量命令"
	case actProxy:
		return "设置代理"
	case actDelete:
		return "删除"
	case actCreate:
		return "新建"
	}
	return "操作"
}

// checkAction reports whether action a can run on inst right now.
// busyOp is the title of the task that currently holds inst ("" when idle);
// an instance with a running task never accepts a conflicting operation.
func checkAction(a action, inst service.Instance, busyOp string) (bool, string) {
	if busyOp != "" {
		return false, fmt.Sprintf("正在执行「%s」", busyOp)
	}
	st := inst.State
	switch a {
	case actStart:
		if st == service.Failed && inst.Port > 0 {
			return false, "启动失败的进程尚未退出，请先停止"
		}
		switch st {
		case service.Running:
			return false, "已在运行"
		case service.Starting:
			return false, "正在启动"
		}
	case actStop:
		if st != service.Running && st != service.Starting && !(st == service.Failed && inst.Port > 0) {
			return false, "未在运行"
		}
	case actClone:
		if st == service.Running || st == service.Starting || inst.Port > 0 {
			return false, "克隆前需先停止"
		}
	case actScreen, actInstallAPK, actShell:
		switch st {
		case service.Running:
		case service.Starting:
			return false, "尚未启动完成"
		default:
			return false, "未运行"
		}
	}
	return true, ""
}

// skippedInstance is a selected instance that cannot take the action, with the reason.
type skippedInstance struct {
	inst   service.Instance
	reason string
}

// actionPlan splits a selection into the instances that can take an action and those that cannot.
type actionPlan struct {
	action   action
	total    int
	eligible []service.Instance
	skipped  []skippedInstance
}

func planAction(a action, insts []service.Instance, busy map[string]string) actionPlan {
	p := actionPlan{action: a, total: len(insts)}
	for _, inst := range insts {
		if ok, reason := checkAction(a, inst, busy[inst.ID]); ok {
			p.eligible = append(p.eligible, inst)
		} else {
			p.skipped = append(p.skipped, skippedInstance{inst: inst, reason: reason})
		}
	}
	return p
}

// mixed reports a selection where only part of the instances can take the action.
func (p actionPlan) mixed() bool { return len(p.eligible) > 0 && len(p.skipped) > 0 }

// buttonLabel shows "启动 (2/5)" when only some of the selection can take the action.
func (p actionPlan) buttonLabel(base string) string {
	if p.total > 1 && p.mixed() {
		return fmt.Sprintf("%s (%d/%d)", base, len(p.eligible), p.total)
	}
	return base
}

// disabledHint explains why nothing can be done; "" when the action is available.
func (p actionPlan) disabledHint() string {
	if len(p.eligible) > 0 {
		return ""
	}
	if p.total == 0 {
		return "请先选择实例"
	}
	return p.skipped[0].reason + summarizeSkipped(p.skipped, 1)
}

func summarizeSkipped(sk []skippedInstance, shown int) string {
	if len(sk) <= shown {
		return ""
	}
	return fmt.Sprintf("（另有 %d 台同样不可用）", len(sk)-shown)
}

// pendingRestart reports a running instance whose saved proxy differs from the one it was launched with.
func pendingRestart(inst service.Instance) bool {
	if inst.State != service.Running && inst.State != service.Starting {
		return false
	}
	return strings.TrimSpace(inst.Proxy) != strings.TrimSpace(inst.AppliedProxy)
}

func proxyText(p string) string {
	if strings.TrimSpace(p) == "" {
		return "直连"
	}
	return p
}

// proxyBreakdown groups selected instances by saved proxy, in first-seen order.
type proxyGroup struct {
	proxy string
	count int
}

func proxyBreakdown(insts []service.Instance) []proxyGroup {
	var groups []proxyGroup
	for _, inst := range insts {
		p := strings.TrimSpace(inst.Proxy)
		found := false
		for i := range groups {
			if groups[i].proxy == p {
				groups[i].count++
				found = true
				break
			}
		}
		if !found {
			groups = append(groups, proxyGroup{proxy: p, count: 1})
		}
	}
	return groups
}

func describeProxyGroups(groups []proxyGroup) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, fmt.Sprintf("%s ×%d", proxyText(g.proxy), g.count))
	}
	return strings.Join(parts, "、")
}

// shortImage turns "system-images;android-34;google_apis_playstore;x86_64" into "Android 34 · google_apis_playstore".
func shortImage(pkg string) string {
	parts := strings.Split(pkg, ";")
	if len(parts) >= 3 && strings.HasPrefix(parts[1], "android-") {
		return fmt.Sprintf("Android %s · %s", strings.TrimPrefix(parts[1], "android-"), parts[2])
	}
	return pkg
}

// namesPreview lists up to max instance names, then "…等共 N 台".
func namesPreview(insts []service.Instance, max int) string {
	names := make([]string, 0, len(insts))
	for i, inst := range insts {
		if i >= max {
			break
		}
		names = append(names, inst.Name)
	}
	s := strings.Join(names, "、")
	if len(insts) > max {
		s += fmt.Sprintf(" …等共 %d 台", len(insts))
	}
	return s
}
