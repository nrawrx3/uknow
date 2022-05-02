package uknow

import "errors"

// just being conservative. 4 bits for number and 3 bits for color is enough.
// [unused bits][5 bits for number][4 bits for color]
const (
	numberBitsCount = 5
	colorBitsCount  = 4
	numberMask      = (uint32(1<<numberBitsCount) - 1) << uint32(colorBitsCount)
	colorMask       = (uint32(1<<colorBitsCount) - 1)
)

var ErrInvalidCardColor = errors.New("invalid card color")
var ErrInvalidCardNumber = errors.New("invalid card number")

func (c Card) EncodeUint32() uint32 {
	return (uint32(c.Number) << colorBitsCount) | uint32(c.Color)
}

func DecodeCardFromUint32(x uint32) (Card, error) {
	color := uint32(x & colorMask)
	if color > uint32(Yellow) {
		return Card{}, ErrInvalidCardColor
	}
	number := uint32(x&numberMask) >> colorBitsCount
	if number > uint32(NumberWildDrawFour) {
		return Card{}, ErrInvalidCardNumber
	}
	return Card{Color: Color(color), Number: Number(number)}, nil
}

func MustDecodeCardFromUint32(x uint32) Card {
	card, err := DecodeCardFromUint32(x)
	if err != nil {
		panic(err)
	}
	return card
}

func (c Card) Hash() uint32 {
	return c.EncodeUint32()
}
