// Package canbus 通过 vcan + SocketCAN 提供 CAN 总线后端。
package canbus

import (
	"encoding/binary"
	"fmt"

	"github.com/lansonsam/buslab/internal/model"
)

// 经典 CAN 帧（struct can_frame）在 Linux 上固定 16 字节。
const FrameSize = 16

const (
	flagEFF = 0x80000000
	flagRTR = 0x40000000
	flagERR = 0x20000000
	maskSFF = 0x000007FF
	maskEFF = 0x1FFFFFFF
)

// EncodeFrame 把统一 Frame 编码为 struct can_frame 字节序列。
func EncodeFrame(f model.Frame) ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	id := f.CANID
	if f.Ext {
		id = (id & maskEFF) | flagEFF
	} else {
		id &= maskSFF
	}
	if f.RTR {
		id |= flagRTR
	}
	buf := make([]byte, FrameSize)
	binary.NativeEndian.PutUint32(buf[0:4], id)
	buf[4] = byte(len(f.Data))
	copy(buf[8:], f.Data)
	return buf, nil
}

// DecodeFrame 解析 struct can_frame；错误帧以 Note 标注。
func DecodeFrame(raw []byte) (model.Frame, error) {
	if len(raw) < FrameSize {
		return model.Frame{}, fmt.Errorf("CAN 帧长度不足：%d < %d", len(raw), FrameSize)
	}
	id := binary.NativeEndian.Uint32(raw[0:4])
	dlc := int(raw[4])
	if dlc > model.MaxCANDataLen {
		dlc = model.MaxCANDataLen
	}
	f := model.Frame{
		Kind: model.BusCAN,
		Ext:  id&flagEFF != 0,
		RTR:  id&flagRTR != 0,
	}
	if f.Ext {
		f.CANID = id & maskEFF
	} else {
		f.CANID = id & maskSFF
	}
	if id&flagERR != 0 {
		f.Note = "错误帧"
	}
	if dlc > 0 && !f.RTR {
		f.Data = append([]byte(nil), raw[8:8+dlc]...)
	}
	return f, nil
}
