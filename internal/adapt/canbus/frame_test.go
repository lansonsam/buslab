package canbus

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/lansonsam/buslab/internal/model"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []model.Frame{
		{Kind: model.BusCAN, CANID: 0x123, Data: []byte{1, 2, 3}},
		{Kind: model.BusCAN, CANID: 0x7FF, Data: []byte{0xFF, 0xFE, 0, 0, 0, 0, 0, 1}},
		{Kind: model.BusCAN, CANID: 0x1ABCDEF, Ext: true, Data: []byte{0xAA}},
		{Kind: model.BusCAN, CANID: 0x100, RTR: true},
	}
	for _, in := range cases {
		raw, err := EncodeFrame(in)
		if err != nil {
			t.Fatalf("编码失败：%v", err)
		}
		if len(raw) != FrameSize {
			t.Fatalf("帧长 %d，期望 %d", len(raw), FrameSize)
		}
		out, err := DecodeFrame(raw)
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		if out.CANID != in.CANID || out.Ext != in.Ext || out.RTR != in.RTR {
			t.Fatalf("往返不一致：%+v vs %+v", out, in)
		}
		if in.RTR {
			if len(out.Data) != 0 {
				t.Fatalf("RTR 帧不应带数据：%X", out.Data)
			}
			continue
		}
		if !bytes.Equal(out.Data, in.Data) {
			t.Fatalf("数据不一致：%X vs %X", out.Data, in.Data)
		}
	}
}

func TestEncodeRejectsInvalid(t *testing.T) {
	if _, err := EncodeFrame(model.Frame{Kind: model.BusCAN, CANID: 0x800}); err == nil {
		t.Fatal("标准帧 ID 越界应报错")
	}
	if _, err := EncodeFrame(model.Frame{Kind: model.BusCAN, Data: make([]byte, 9)}); err == nil {
		t.Fatal("超长数据应报错")
	}
}

func TestDecodeErrorFlagAndShortBuffer(t *testing.T) {
	raw := make([]byte, FrameSize)
	binary.NativeEndian.PutUint32(raw[0:4], flagERR|0x20)
	f, err := DecodeFrame(raw)
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if f.Note == "" {
		t.Fatal("错误帧应带 Note 标注")
	}
	if _, err := DecodeFrame(raw[:8]); err == nil {
		t.Fatal("短缓冲应报错")
	}
}

func TestEncodeSetsDLCAndFlags(t *testing.T) {
	raw, err := EncodeFrame(model.Frame{Kind: model.BusCAN, CANID: 0x11, Ext: true, Data: []byte{9, 9}})
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	if raw[4] != 2 {
		t.Fatalf("DLC = %d", raw[4])
	}
	if id := binary.NativeEndian.Uint32(raw[0:4]); id&flagEFF == 0 {
		t.Fatalf("扩展帧标志缺失：%08X", id)
	}
}
