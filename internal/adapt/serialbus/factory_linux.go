//go:build linux

package serialbus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lansonsam/buslab/internal/model"
)

func newFactory(backend model.SerialBackend) PortFactory {
	switch backend {
	case model.SerialBackendTTY0TTY:
		return &ttyFactory{used: map[string]bool{}}
	case model.SerialBackendPTY:
		return &ptyFactory{}
	}
	return nil
}

// ttyFactory 从 tty0tty 提供的 /dev/tnt* 配对中分配端口。
// 约定：偶数号设备给外部工具，奇数号设备由 Hub 持有。
type ttyFactory struct {
	mu   sync.Mutex
	used map[string]bool
}

func (f *ttyFactory) Backend() model.SerialBackend { return model.SerialBackendTTY0TTY }

func (f *ttyFactory) Open(spec model.BusSpec, _ model.NodeID) (Port, error) {
	pairs, err := tntPairs()
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	var external, internal string
	for _, p := range pairs {
		if !f.used[p[1]] {
			external, internal = p[0], p[1]
			f.used[internal] = true
			break
		}
	}
	f.mu.Unlock()
	if internal == "" {
		return nil, fmt.Errorf("tty0tty 端口已全部占用（共 %d 对），可加载更多实例或改用 pty", len(pairs))
	}

	file, err := openTTY(internal, spec.Serial)
	if err != nil {
		f.release(internal)
		return nil, err
	}
	return &ttyPort{file: file, external: external, onClose: func() { f.release(internal) }}, nil
}

func (f *ttyFactory) release(internal string) {
	f.mu.Lock()
	delete(f.used, internal)
	f.mu.Unlock()
}

func tntPairs() ([][2]string, error) {
	matches, err := filepath.Glob("/dev/tnt*")
	if err != nil {
		return nil, fmt.Errorf("枚举 /dev/tnt* 失败：%w", err)
	}
	index := map[int]string{}
	for _, m := range matches {
		n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(m), "tnt"))
		if err != nil {
			continue
		}
		index[n] = m
	}
	var keys []int
	for k := range index {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var pairs [][2]string
	for _, k := range keys {
		if k%2 != 0 {
			continue
		}
		if peer, ok := index[k+1]; ok {
			pairs = append(pairs, [2]string{index[k], peer})
		}
	}
	if len(pairs) == 0 {
		return nil, errors.New("未找到成对的 /dev/tnt* 设备")
	}
	return pairs, nil
}

// ptyFactory 通过 /dev/ptmx 动态创建伪终端对。
type ptyFactory struct{}

func (f *ptyFactory) Backend() model.SerialBackend { return model.SerialBackendPTY }

func (f *ptyFactory) Open(spec model.BusSpec, _ model.NodeID) (Port, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 /dev/ptmx 失败：%w", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("解锁伪终端失败：%w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("获取伪终端编号失败：%w", err)
	}
	slave := fmt.Sprintf("/dev/pts/%d", n)
	if err := configureTermios(fd, spec.Serial); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &ttyPort{
		file:     os.NewFile(uintptr(fd), slave+"(master)"),
		external: slave,
		tolerant: true,
	}, nil
}

func openTTY(path string, params model.SerialParams) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 %s 失败：%w", path, err)
	}
	if err := configureTermios(fd, params); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

var baudCodes = map[int]uint32{
	1200: unix.B1200, 2400: unix.B2400, 4800: unix.B4800, 9600: unix.B9600,
	19200: unix.B19200, 38400: unix.B38400, 57600: unix.B57600, 115200: unix.B115200,
	230400: unix.B230400, 460800: unix.B460800, 921600: unix.B921600,
}

// configureTermios 把端口设为 raw 模式并套用线路参数。
func configureTermios(fd int, p model.SerialParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	baud, ok := baudCodes[p.BaudRate]
	if !ok {
		return fmt.Errorf("不支持的波特率 %d", p.BaudRate)
	}
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("读取 termios 失败：%w", err)
	}
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.PARODD | unix.CSTOPB | unix.CBAUD | unix.CRTSCTS
	t.Cflag |= unix.CREAD | unix.CLOCAL | baud

	switch p.DataBits {
	case 5:
		t.Cflag |= unix.CS5
	case 6:
		t.Cflag |= unix.CS6
	case 7:
		t.Cflag |= unix.CS7
	default:
		t.Cflag |= unix.CS8
	}
	switch p.Parity {
	case model.ParityEven:
		t.Cflag |= unix.PARENB
	case model.ParityOdd:
		t.Cflag |= unix.PARENB | unix.PARODD
	}
	if p.StopBits == 2 {
		t.Cflag |= unix.CSTOPB
	}
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		return fmt.Errorf("设置 termios 失败：%w", err)
	}
	return nil
}

// ttyPort 是 Hub 侧的串口句柄。tolerant 用于 pty：外部程序关闭从端会让
// master 读返回 EIO，此时不应视为致命错误。
type ttyPort struct {
	file     *os.File
	external string
	tolerant bool
	onClose  func()

	closeOnce sync.Once
	closeErr  error
}

func (p *ttyPort) Read(b []byte) (int, error) {
	n, err := p.file.Read(b)
	if err != nil && p.tolerant && errors.Is(err, unix.EIO) {
		time.Sleep(100 * time.Millisecond)
		return n, nil
	}
	return n, err
}

func (p *ttyPort) Write(b []byte) (int, error) {
	n, err := p.file.Write(b)
	if err != nil && p.tolerant && errors.Is(err, unix.EIO) {
		return n, nil
	}
	return n, err
}

func (p *ttyPort) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.file.Close()
		if p.onClose != nil {
			p.onClose()
		}
	})
	return p.closeErr
}

func (p *ttyPort) External() string { return p.external }
