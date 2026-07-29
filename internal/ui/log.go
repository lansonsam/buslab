package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/lansonsam/buslab/internal/model"
)

const allBusesOption = "全部总线"

type logRow struct {
	bus     model.BusID
	columns [6]string
}

// logPanel 是右下角的流量日志：环形缓冲 + 可暂停 + 按总线过滤。
type logPanel struct {
	limit  int
	rows   []logRow
	view   []int
	filter model.BusID
	paused bool
	skiped int

	table   *widget.Table
	filterS *widget.Select
	pauseB  *widget.Button
	status  *widget.Label
	busName map[string]model.BusID
	root    fyne.CanvasObject
}

var logHeaders = [6]string{"序号", "时间", "总线", "节点", "方向", "内容"}

var logColumnWidths = [6]float32{62, 96, 128, 112, 56, 420}

func newLogPanel(limit int) *logPanel {
	if limit <= 0 {
		limit = 5000
	}
	l := &logPanel{limit: limit, busName: map[string]model.BusID{}}

	l.table = widget.NewTable(
		func() (int, int) { return len(l.view), len(logHeaders) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label, ok := cell.(*widget.Label)
			if !ok || id.Row < 0 || id.Row >= len(l.view) {
				return
			}
			row := l.rows[l.view[id.Row]]
			label.SetText(row.columns[id.Col])
		},
	)
	l.table.ShowHeaderRow = true
	l.table.CreateHeader = func() fyne.CanvasObject {
		return widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	l.table.UpdateHeader = func(id widget.TableCellID, cell fyne.CanvasObject) {
		label, ok := cell.(*widget.Label)
		if !ok || id.Col < 0 || id.Col >= len(logHeaders) {
			return
		}
		label.SetText(logHeaders[id.Col])
	}
	for i, w := range logColumnWidths {
		l.table.SetColumnWidth(i, w)
	}

	l.filterS = widget.NewSelect([]string{allBusesOption}, func(name string) {
		l.setFilter(l.busName[name])
	})
	l.filterS.SetSelected(allBusesOption)

	l.pauseB = widget.NewButtonWithIcon("暂停", theme.MediaPauseIcon(), l.togglePause)
	clear := widget.NewButtonWithIcon("清空", theme.DeleteIcon(), l.clear)
	l.status = widget.NewLabel("")
	l.updateStatus()

	bar := container.NewHBox(l.pauseB, clear, widget.NewLabel("过滤："), l.filterS)
	l.root = container.NewBorder(bar, l.status, nil, nil, l.table)
	return l
}

func (l *logPanel) content() fyne.CanvasObject { return l.root }

// setBuses 同步过滤下拉框选项。
func (l *logPanel) setBuses(buses []*model.Bus) {
	options := []string{allBusesOption}
	names := map[string]model.BusID{allBusesOption: ""}
	selected := allBusesOption
	for _, b := range buses {
		label := fmt.Sprintf("%s（%s）", b.Name, b.Type.Label())
		options = append(options, label)
		names[label] = b.ID
		if b.ID == l.filter {
			selected = label
		}
	}
	l.busName = names
	l.filterS.Options = options
	if names[selected] != l.filter {
		l.filter = ""
		selected = allBusesOption
		l.rebuildView()
	}
	l.filterS.SetSelected(selected)
	l.filterS.Refresh()
}

func (l *logPanel) setFilter(bus model.BusID) {
	if l.filter == bus {
		return
	}
	l.filter = bus
	l.rebuildView()
}

func (l *logPanel) togglePause() {
	l.paused = !l.paused
	if l.paused {
		l.pauseB.SetText("继续")
		l.pauseB.SetIcon(theme.MediaPlayIcon())
	} else {
		l.pauseB.SetText("暂停")
		l.pauseB.SetIcon(theme.MediaPauseIcon())
		l.skiped = 0
	}
	l.updateStatus()
}

func (l *logPanel) clear() {
	l.rows = nil
	l.view = nil
	l.skiped = 0
	l.table.Refresh()
	l.updateStatus()
}

func (l *logPanel) append(f model.Frame, busName, nodeName string) {
	if l.paused {
		l.skiped++
		l.updateStatus()
		return
	}
	summary := f.Summary()
	if f.Note != "" {
		if summary == "" {
			summary = f.Note
		} else {
			summary += "  ⚠ " + f.Note
		}
	}
	row := logRow{bus: f.Bus, columns: [6]string{
		fmt.Sprintf("%d", f.Seq),
		f.Time.Format("15:04:05.000"),
		busName,
		nodeName,
		f.Dir.Label(),
		summary,
	}}
	l.rows = append(l.rows, row)
	if len(l.rows) > l.limit {
		drop := len(l.rows) - l.limit
		l.rows = l.rows[drop:]
		l.rebuildView()
	} else if l.filter == "" || l.filter == row.bus {
		l.view = append(l.view, len(l.rows)-1)
	}
	l.table.Refresh()
	l.table.ScrollToBottom()
	l.updateStatus()
}

func (l *logPanel) rebuildView() {
	l.view = l.view[:0]
	for i, row := range l.rows {
		if l.filter == "" || l.filter == row.bus {
			l.view = append(l.view, i)
		}
	}
	l.table.Refresh()
	l.updateStatus()
}

func (l *logPanel) updateStatus() {
	text := fmt.Sprintf("共 %d 条（上限 %d）· 当前显示 %d 条", len(l.rows), l.limit, len(l.view))
	if l.paused {
		text += fmt.Sprintf(" · 已暂停，期间丢弃 %d 条", l.skiped)
	}
	l.status.SetText(text)
}

func (l *logPanel) rowCount() int { return len(l.rows) }
