package ui

import (
	"image/color"

	"github.com/lansonsam/buslab/internal/model"
)

// 总线配色：CAN 蓝、485 橙、422 绿、232 灰（克制的工控风，不使用高饱和霓虹色）。
var busColors = map[model.BusType]color.NRGBA{
	model.BusCAN:   {R: 0x3B, G: 0x82, B: 0xF6, A: 0xFF},
	model.BusRS485: {R: 0xEA, G: 0x8C, B: 0x2E, A: 0xFF},
	model.BusRS422: {R: 0x3E, G: 0xA5, B: 0x6D, A: 0xFF},
	model.BusRS232: {R: 0x94, G: 0xA3, B: 0xB8, A: 0xFF},
}

var (
	nodeFill      = color.NRGBA{R: 0x2A, G: 0x2F, B: 0x37, A: 0xFF}
	nodeFillIdle  = color.NRGBA{R: 0x23, G: 0x27, B: 0x2E, A: 0xFF}
	nodeStroke    = color.NRGBA{R: 0x6B, G: 0x72, B: 0x80, A: 0xFF}
	selectStroke  = color.NRGBA{R: 0xE5, G: 0xE7, B: 0xEB, A: 0xFF}
	textColor     = color.NRGBA{R: 0xE5, G: 0xE7, B: 0xEB, A: 0xFF}
	mutedColor    = color.NRGBA{R: 0x9C, G: 0xA3, B: 0xAF, A: 0xFF}
	canvasBgColor = color.NRGBA{R: 0x18, G: 0x1B, B: 0x20, A: 0xFF}
)

func busColor(t model.BusType) color.NRGBA {
	if c, ok := busColors[t]; ok {
		return c
	}
	return nodeStroke
}

func fade(c color.NRGBA, alpha uint8) color.NRGBA {
	c.A = alpha
	return c
}
