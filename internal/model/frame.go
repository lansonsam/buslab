package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Direction string

const (
	DirTx Direction = "tx"
	DirRx Direction = "rx"
)

func (d Direction) Label() string {
	if d == DirTx {
		return "发送"
	}
	return "接收"
}

const (
	MaxCANStandardID = 0x7FF
	MaxCANExtendedID = 0x1FFFFFFF
	MaxCANDataLen    = 8
)

// Frame 是统一的流量事件，CAN 与串口共用。
type Frame struct {
	Seq   uint64
	Time  time.Time
	Bus   BusID
	Node  NodeID
	Dir   Direction
	Kind  BusType
	CANID uint32
	Ext   bool
	RTR   bool
	Data  []byte
	Note  string
}

func (f Frame) Validate() error {
	if f.Kind == BusCAN {
		limit := uint32(MaxCANStandardID)
		if f.Ext {
			limit = MaxCANExtendedID
		}
		if f.CANID > limit {
			return fmt.Errorf("CAN ID 0x%X 超出范围（上限 0x%X）", f.CANID, limit)
		}
		if len(f.Data) > MaxCANDataLen {
			return fmt.Errorf("CAN 数据长度 %d 超出 %d 字节", len(f.Data), MaxCANDataLen)
		}
		return nil
	}
	if len(f.Data) == 0 {
		return fmt.Errorf("发送内容为空")
	}
	return nil
}

func (f Frame) Summary() string {
	if f.Kind == BusCAN {
		width := 3
		if f.Ext {
			width = 8
		}
		head := fmt.Sprintf("ID=0x%0*X [%d]", width, f.CANID, len(f.Data))
		if f.RTR {
			return head + " RTR"
		}
		if len(f.Data) == 0 {
			return head
		}
		return head + " " + FormatHex(f.Data)
	}
	hex := FormatHex(f.Data)
	if ascii := PrintableASCII(f.Data); ascii != "" {
		return fmt.Sprintf("%s  |%s|", hex, ascii)
	}
	return hex
}

func FormatHex(data []byte) string {
	var sb strings.Builder
	for i, b := range data {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(fmt.Sprintf("%02X", b))
	}
	return sb.String()
}

// PrintableASCII 把不可打印字节替换为点，全不可打印时返回空串。
func PrintableASCII(data []byte) string {
	var sb strings.Builder
	printable := false
	for _, b := range data {
		if b < 0x80 && unicode.IsPrint(rune(b)) {
			sb.WriteByte(b)
			printable = true
		} else {
			sb.WriteByte('.')
		}
	}
	if !printable {
		return ""
	}
	return sb.String()
}

var hexSeparators = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range " \t\r\n,-:;_" {
		m[r] = true
	}
	return m
}()

// ParseHexBytes 解析宽松的十六进制输入："11 22", "0x11,0x22", "1122", "11-22:33"。
func ParseHexBytes(s string) ([]byte, error) {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if hexSeparators[r] {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()

	var out []byte
	for _, tok := range tokens {
		t := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(tok), "0x"), "\\x")
		if t == "" {
			continue
		}
		if len(t)%2 == 1 {
			if len(tokens) == 1 {
				return nil, fmt.Errorf("十六进制字符数必须为偶数：%q", tok)
			}
			t = "0" + t
		}
		for i := 0; i < len(t); i += 2 {
			v, err := strconv.ParseUint(t[i:i+2], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("非法十六进制 %q", tok)
			}
			out = append(out, byte(v))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未解析到任何字节")
	}
	return out, nil
}

// ParseCANID 接受十六进制（默认、可带 0x）形式的 CAN ID。
func ParseCANID(s string) (uint32, error) {
	t := strings.TrimSpace(strings.ToLower(s))
	t = strings.TrimPrefix(t, "0x")
	if t == "" {
		return 0, fmt.Errorf("CAN ID 不能为空")
	}
	v, err := strconv.ParseUint(t, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("非法 CAN ID %q", s)
	}
	return uint32(v), nil
}
