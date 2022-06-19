package cell

import (
	"encoding/binary"
	"errors"
	"math/big"

	"github.com/xssnick/tonutils-go/address"
)

type Builder struct {
	bitsSz int
	data   []byte

	// store it as slice of pointers to make indexing logic cleaner on parse,
	// from outside it should always come as object to not have problems
	refs []*Cell
}

func (b *Builder) MustStoreCoins(value uint64) *Builder {
	err := b.StoreCoins(value)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreCoins(value uint64) error {
	return b.StoreBigCoins(new(big.Int).SetUint64(value))
}

func (b *Builder) MustStoreBigCoins(value *big.Int) *Builder {
	err := b.StoreBigCoins(value)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreBigCoins(value *big.Int) error {
	// varInt 16 https://github.com/ton-blockchain/ton/blob/24dc184a2ea67f9c47042b4104bbb4d82289fac1/crypto/block/block-parse.cpp#L319
	ln := (value.BitLen() + 7) >> 3
	if ln >= 16 {
		return ErrTooBigValue
	}

	err := b.StoreUInt(uint64(ln), 4)
	if err != nil {
		return err
	}

	err = b.StoreBigInt(value, ln*8)
	if err != nil {
		return err
	}

	return nil
}

func (b *Builder) MustStoreUInt(value uint64, sz int) *Builder {
	err := b.StoreUInt(value, sz)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreUInt(value uint64, sz int) error {
	if sz > 64 {
		return b.StoreBigInt(new(big.Int).SetUint64(value), sz)
	}

	value <<= 64 - sz
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, value)

	return b.StoreSlice(buf, sz)
}

func (b *Builder) MustStoreBoolBit(value bool) *Builder {
	err := b.StoreBoolBit(value)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreBoolBit(value bool) error {
	var i uint64
	if value {
		i = 1
	}
	return b.StoreBigInt(new(big.Int).SetUint64(i), 1)
}

func (b *Builder) MustStoreBigInt(value *big.Int, sz int) *Builder {
	err := b.StoreBigInt(value, sz)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreBigInt(value *big.Int, sz int) error {
	if value.BitLen() > 256 {
		panic(ErrTooBigValue.Error())
	}

	if sz > 256 {
		panic(ErrTooBigSize.Error())
	}

	bytes := value.Bytes()

	partByte := 0
	if sz%8 != 0 {
		partByte = 1
	}
	bytesToUse := (sz / 8) + partByte

	if len(bytes) < bytesToUse {
		// add zero bits to fit requested size
		bytes = append(make([]byte, bytesToUse-len(bytes)), bytes...)
	}

	// check is value uses full bytes
	if offset := sz % 8; offset > 0 {
		add := byte(0)
		// move bits to left side of bytes to fit into size
		for i := len(bytes) - 1; i >= 0; i-- {
			toMove := bytes[i] & (0xFF << offset) // get last bits
			bytes[i] <<= 8 - offset               // move first bits to last
			bytes[i] += add
			add = toMove >> offset
		}
	}

	return b.StoreSlice(bytes, sz)
}

func (b *Builder) MustStoreAddr(addr *address.Address) *Builder {
	err := b.StoreAddr(addr)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreAddr(addr *address.Address) error {
	if addr == nil {
		return b.StoreUInt(0, 2)
	}

	// addr std
	err := b.StoreUInt(0b10, 2)
	if err != nil {
		return err
	}

	// anycast
	err = b.StoreUInt(0b0, 1)
	if err != nil {
		return err
	}

	err = b.StoreUInt(uint64(addr.Workchain()), 8)
	if err != nil {
		return err
	}

	err = b.StoreSlice(addr.Data(), 256)
	if err != nil {
		return err
	}

	return nil
}

func (b *Builder) MustStoreMaybeRef(ref *Cell) *Builder {
	err := b.StoreMaybeRef(ref)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreMaybeRef(ref *Cell) error {
	if ref == nil {
		return b.StoreUInt(0, 1)
	}

	// we need early checks to do 2 stores atomically
	if len(b.refs) > 4 {
		return ErrTooMuchRefs
	}
	if b.bitsSz+1 >= 1024 {
		return ErNotFit1024
	}

	b.MustStoreUInt(1, 1).MustStoreRef(ref)
	return nil
}

func (b *Builder) MustStoreRef(ref *Cell) *Builder {
	err := b.StoreRef(ref)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreRef(ref *Cell) error {
	if len(b.refs) > 4 {
		return ErrTooMuchRefs
	}

	if ref == nil {
		return errors.New("ref cannot be nil")
	}

	b.refs = append(b.refs, ref)

	return nil
}

func (b *Builder) MustStoreSlice(bytes []byte, sz int) *Builder {
	err := b.StoreSlice(bytes, sz)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreSlice(bytes []byte, sz int) error {
	oneMore := 0
	if sz%8 > 0 {
		oneMore = 1
	}

	if len(bytes) < sz/8+oneMore {
		return ErrSmallSlice
	}

	if b.bitsSz+sz >= 1024 {
		return ErNotFit1024
	}

	leftSz := sz
	unusedBits := 8 - (b.bitsSz % 8)

	offset := 0
	for leftSz > 0 {
		bits := 8
		if leftSz < 8 {
			bits = leftSz
		}
		leftSz -= bits

		// if previous byte was not filled, we need to move bits to fill it
		if unusedBits != 8 {
			b.data[len(b.data)-1] += bytes[offset] >> (8 - unusedBits)
			if bits > unusedBits {
				b.data = append(b.data, bytes[offset]<<unusedBits)
			}
			offset++
			continue
		}

		// clear unused part of byte if needed
		b.data = append(b.data, bytes[offset]&(0xFF<<(8-bits)))
		offset++
	}

	b.bitsSz += sz

	return nil
}

func (b *Builder) MustStoreBuilder(builder *Builder) *Builder {
	err := b.StoreBuilder(builder)
	if err != nil {
		panic(err)
	}
	return b
}

func (b *Builder) StoreBuilder(builder *Builder) error {
	if len(b.refs)+len(builder.refs) > 4 {
		return ErrTooMuchRefs
	}

	if b.bitsSz+builder.bitsSz >= 1024 {
		return ErrTooMuchRefs
	}

	b.refs = append(b.refs, builder.refs...)
	b.MustStoreSlice(builder.data, builder.bitsSz)

	return nil
}

func (b *Builder) RefsUsed() int {
	return len(b.refs)
}

func (b *Builder) BitsUsed() int {
	return b.bitsSz
}

func (b *Builder) BitsLeft() int {
	return 1023 - b.bitsSz
}

func (b *Builder) RefsLeft() int {
	return 4 - len(b.refs)
}

func (b *Builder) Copy() *Builder {
	// copy data
	data := append([]byte{}, b.data...)

	return &Builder{
		bitsSz: b.bitsSz,
		data:   data,
		refs:   b.refs,
	}
}

func BeginCell() *Builder {
	return &Builder{}
}

func (b *Builder) EndCell() *Cell {
	// copy data
	data := append([]byte{}, b.data...)

	return &Cell{
		bitsSz: b.bitsSz,
		data:   data,
		refs:   b.refs,
	}
}
