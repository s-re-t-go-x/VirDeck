package mygonative

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"virdeck/core/service"
)

const (
	taskWorkers  = 4
	maxKeptTasks = 30
)

type itemStatus int

const (
	itemPending itemStatus = iota
	itemRunning
	itemOK
	itemFailed
)

// target is one unit of work in a task: usually one instance.
type target struct {
	id   string // instance ID; "" for tasks that are not tied to an existing instance
	name string
}

func targetsOf(insts []service.Instance) []target {
	out := make([]target, 0, len(insts))
	for _, inst := range insts {
		out = append(out, target{id: inst.ID, name: inst.Name})
	}
	return out
}

type taskItem struct {
	target
	status itemStatus
	err    string
}

// task is a batch operation with per-instance progress. All fields are only
// read or written on the UI thread (through viewModel.post).
type task struct {
	id        int
	title     string
	act       action
	items     []*taskItem
	started   time.Time
	finished  bool
	retryable bool
	op        func(ctx context.Context, t target) error
	onDone    func(t *task)
}

// counts returns how many items have finished, succeeded and failed.
func (t *task) counts() (done, ok, failed int) {
	for _, it := range t.items {
		switch it.status {
		case itemOK:
			ok++
			done++
		case itemFailed:
			failed++
			done++
		}
	}
	return done, ok, failed
}

func (t *task) summary() string {
	done, ok, failed := t.counts()
	return fmt.Sprintf("完成 %d/%d · 成功 %d · 失败 %d", done, len(t.items), ok, failed)
}

func (t *task) progress() float64 {
	if len(t.items) == 0 {
		return 1
	}
	done, _, _ := t.counts()
	return float64(done) / float64(len(t.items))
}

func (t *task) failedItems() []*taskItem {
	var out []*taskItem
	for _, it := range t.items {
		if it.status == itemFailed {
			out = append(out, it)
		}
	}
	return out
}

// startTask registers a task, marks its instances busy and runs op for each target
// with a small worker pool. It must be called on the UI thread.
func (vm *viewModel) startTask(title string, act action, targets []target, retryable bool,
	op func(ctx context.Context, t target) error, onDone func(t *task)) *task {
	vm.nextTaskID++
	t := &task{
		id:        vm.nextTaskID,
		title:     title,
		act:       act,
		started:   time.Now(),
		retryable: retryable,
		op:        op,
		onDone:    onDone,
	}
	for _, tg := range targets {
		t.items = append(t.items, &taskItem{target: tg})
		if tg.id != "" {
			vm.busy[tg.id] = title
		}
	}
	vm.tasks = append(vm.tasks, t)
	if len(vm.tasks) > maxKeptTasks {
		vm.tasks = vm.tasks[len(vm.tasks)-maxKeptTasks:]
	}
	vm.appendLog(fmt.Sprintf("[%s] 开始任务「%s」，共 %d 项", time.Now().Format("15:04:05"), title, len(t.items)))
	go vm.runTask(t)
	return t
}

func (vm *viewModel) runTask(t *task) {
	ctx := context.Background()
	ch := make(chan *taskItem)
	var wg sync.WaitGroup
	for range min(taskWorkers, max(1, len(t.items))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				vm.post(func() { it.status = itemRunning })
				err := t.op(ctx, it.target)
				vm.post(func() {
					if err != nil {
						it.status = itemFailed
						it.err = oneLine(err.Error())
						vm.appendLog(fmt.Sprintf("%s / %s 失败：%v", t.title, it.name, err))
					} else {
						it.status = itemOK
					}
					if it.id != "" && vm.busy[it.id] == t.title {
						delete(vm.busy, it.id)
					}
				})
			}
		}()
	}
	for _, it := range t.items {
		ch <- it
	}
	close(ch)
	wg.Wait()

	vm.post(func() {
		t.finished = true
		_, _, failed := t.counts()
		line := fmt.Sprintf("[%s] 任务「%s」结束：%s", time.Now().Format("15:04:05"), t.title, t.summary())
		if failed > 0 {
			line += "（可在任务面板查看原因并重试）"
		}
		vm.appendLog(line)
		if t.onDone != nil {
			t.onDone(t)
		}
	})
}

// retryFailed starts a new task for the failed items of a finished task, skipping
// the ones that cannot take the action in their current state.
func (vm *viewModel) retryFailed(t *task) {
	if !t.retryable || !t.finished {
		return
	}
	var insts []service.Instance
	for _, it := range t.failedItems() {
		if inst, ok := vm.findInstance(it.id); ok {
			insts = append(insts, inst)
		}
	}
	p := planAction(t.act, insts, vm.busy)
	if len(p.eligible) == 0 {
		reason := "实例已不存在"
		if len(p.skipped) > 0 {
			reason = p.skipped[0].reason
		}
		vm.appendLog(fmt.Sprintf("[%s] 任务「%s」没有可重试的项目：%s", time.Now().Format("15:04:05"), t.title, reason))
		return
	}
	vm.startTask(t.title, t.act, targetsOf(p.eligible), true, t.op, t.onDone)
}

func (vm *viewModel) findInstance(id string) (service.Instance, bool) {
	for _, inst := range vm.instances {
		if inst.ID == id {
			return inst, true
		}
	}
	return service.Instance{}, false
}

func (vm *viewModel) runningTaskCount() int {
	n := 0
	for _, t := range vm.tasks {
		if !t.finished {
			n++
		}
	}
	return n
}

func (vm *viewModel) latestTask() *task {
	if len(vm.tasks) == 0 {
		return nil
	}
	return vm.tasks[len(vm.tasks)-1]
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", " | ")
	return strings.ReplaceAll(s, "\n", " | ")
}

func statusMark(st itemStatus) string {
	switch st {
	case itemOK:
		return "✓"
	case itemFailed:
		return "✗"
	case itemRunning:
		return "◌"
	}
	return "·"
}
