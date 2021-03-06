// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.
//编码器，把GO的数据结构序列化为字节数组
package rlp

import (
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sync"
)

var (
	// Common encoded values.
	// These are useful when implementing EncodeRLP.
	// 实现EncodeRLP接口需要
	EmptyString = []byte{0x80}  //128，定义了序列化时候的空字符串，空的时候对应的是编码128。
	EmptyList   = []byte{0xC0}  //192，定义了序列化时候的空集合，空的时候对应的编码192
)

// Encoder is implemented by types that require custom
// encoding rules or want to encode private fields.
type Encoder interface {
	// EncodeRLP should write the RLP encoding of its receiver to w.
	// If the implementation is a pointer method, it may also be
	// called for nil pointers.
	//
	// Implementations should generate valid RLP. The data written is
	// not verified at the moment, but a future version might. It is
	// recommended to write only a single value but writing multiple
	// values or no value at all is also permitted.
	EncodeRLP(io.Writer) error
}

// Encode writes the RLP encoding of val to w. Note that Encode may
// perform many small writes in some cases. Consider making w
// buffered.
//
// Encode uses the following type-dependent encoding rules:
//
// If the type implements the Encoder interface, Encode calls
// EncodeRLP. This is true even for nil pointers, please see the
// documentation for Encoder.
//
// To encode a pointer, the value being pointed to is encoded. For nil
// pointers, Encode will encode the zero value of the type. A nil
// pointer to a struct type always encodes as an empty RLP list.
// A nil pointer to an array encodes as an empty list (or empty string
// if the array has element type byte).
//
// Struct values are encoded as an RLP list of all their encoded
// public fields. Recursive struct types are supported.
//
// To encode slices and arrays, the elements are encoded as an RLP
// list of the value's elements. Note that arrays and slices with
// element type uint8 or byte are always encoded as an RLP string.
//
// A Go string is encoded as an RLP string.
//
// An unsigned integer value is encoded as an RLP string. Zero always
// encodes as an empty RLP string. Encode also supports *big.Int.
//
// An interface value encodes as the value contained in the interface.
//
// Boolean values are not supported, nor are signed integers, floating
// point numbers, maps, channels and functions.

//val 是一个任意数据类型
//该方法用于将数据都保存在encbuf中
func Encode(w io.Writer, val interface{}) error {
	//语法说明，断言w.(*encbuf)的具体类型是否为*encbuf，是，则返回给outer=*encbuf,并且ok为true；否，则返回outer=nil，并且ok为false
	if outer, ok := w.(*encbuf); ok {
		// Encode was called by some type's EncodeRLP.
		// Avoid copying by writing to the outer encbuf directly.
		// encbuf:类似一个缓存结构
		// encbuf是encode buffer的简写。encbuf出现在Encode方法，和很多Writer方法中。顾名思义，这个是在encode的过程中充当buffer的作用。下面先看看encbuf的定义。
		return outer.encode(val) //原始数据进行编码，刚编码好的数据是放在字符串中的
	}
	eb := encbufPool.Get().(*encbuf) //从池子获取一个用于存储完整编码数据的空间，若没有该空间，则新建
	defer encbufPool.Put(eb) //当return后才会执行
	eb.reset()
	if err := eb.encode(val); err != nil {//原始数据进行编码，刚编码好的数据是放在字符串中的
		return err
	}
	return eb.toWriter(w)
}

// EncodeBytes returns the RLP encoding of val.
// Please see the documentation of Encode for the encoding rules.
// 将任意数据返回为 RLP编码，这个为编码的原始入口
// 该方法与上面的Encode()方法类似，但上面的只是将编码结果保存在w中了

func EncodeToBytes(val interface{}) ([]byte, error) {
	eb := encbufPool.Get().(*encbuf)  //从池子获取一个用于存储完整编码数据的空间，若没有该空间，则新建
	defer encbufPool.Put(eb)  //return 结束之后才会执行
	eb.reset()  //初始化池子里的对象
	if err := eb.encode(val); err != nil { //原始数据进行编码，刚编码好的数据是放在字符串中的
		return nil, err
	}
	return eb.toBytes(), nil //将编码后的字符串数据放在byte[]中，
}

// EncodeReader returns a reader from which the RLP encoding of val
// can be read. The returned size is the total size of the encoded
// data.
//
// Please see the documentation of Encode for the encoding rules.
func EncodeToReader(val interface{}) (size int, r io.Reader, err error) {
	eb := encbufPool.Get().(*encbuf)
	eb.reset()
	if err := eb.encode(val); err != nil {
		return 0, nil, err
	}
	return eb.size(), &encReader{buf: eb}, nil
}

type encbuf struct {
	str     []byte      // string data, contains everything except list headers 包含了所有的内容，除了列表的头部
	lheads  []*listhead // all list headers  列表头部，str中编码的数据是原始的所有数据，并没有包含其余辅助数据，因此用listhead来记录每个原始数据在哪部分
	lhsize  int         // sum of sizes of all encoded list headers
	sizebuf []byte      // 9-byte auxiliary buffer for uint encoding  9个字节大小的辅助buffer，专门用来处理uint的编码的
}

type listhead struct {
	offset int // index of this header in string data offset字段记录了列表数据在str字段的哪个位置
	size   int // total size of encoded data (including list headers) size字段记录了包含列表头的编码后的数据的总长度。
}

// encode writes head to the given buffer, which must be at least
// 9 bytes long. It returns the encoded bytes.
func (head *listhead) encode(buf []byte) []byte {
	return buf[:puthead(buf, 0xC0, 0xF7, uint64(head.size))]
}

// headsize returns the size of a list or string header
// for a value of the given size.
func headsize(size uint64) int {
	if size < 56 {
		return 1
	}
	return 1 + intsize(size)
}

// puthead writes a list or string header to buf.
// buf must be at least 9 bytes long.
// 把头部插入最终编码序列中
func puthead(buf []byte, smalltag, largetag byte, size uint64) int {
	if size < 56 {
		buf[0] = smalltag + byte(size) //buf[0]，对应于完整编码序列的当前待插入位置
		return 1
	} else {
		sizesize := putint(buf[1:], size)
		buf[0] = largetag + byte(sizesize)
		return sizesize + 1
	}
}

// encbufs are pooled.
// 为encbuf中的缓存字段设置缓存大小，新建一个9字节的缓存，用于缓存待编码数据（部分）
// 新建一个encbuf，它里面存放一个待编码的完整数据
var encbufPool = sync.Pool{
	New: func() interface{} { return &encbuf{sizebuf: make([]byte, 9)} },
}

//w中的sizebuf没有必要初始化
func (w *encbuf) reset() {
	w.lhsize = 0
	if w.str != nil {
		w.str = w.str[:0]
	}
	if w.lheads != nil {
		w.lheads = w.lheads[:0]
	}
}

// encbuf implements io.Writer so it can be passed it into EncodeRLP.
func (w *encbuf) Write(b []byte) (int, error) {
	w.str = append(w.str, b...)
	return len(b), nil
}

//编码入口
func (w *encbuf) encode(val interface{}) error {
	rval := reflect.ValueOf(val)  //获取该数据的值
	ti, err := cachedTypeInfo(rval.Type(), tags{}) //根据数据类型来处理
	if err != nil {
		return err
	}
	//将编码后的数据都保存到encbuf中
	//此时才真正执行了makeWriter()方法中返回的write方法名对应的操作
	//write为定义的函数类型，函数名不同，但参数类型和返回相同，则都为同类型
	return ti.writer(rval, w)
}

func (w *encbuf) encodeStringHeader(size int) {
	if size < 56 {
		w.str = append(w.str, 0x80+byte(size))
	} else {
		// TODO: encode to w.str directly
		sizesize := putint(w.sizebuf[1:], uint64(size))
		w.sizebuf[0] = 0xB7 + byte(sizesize)
		w.str = append(w.str, w.sizebuf[:sizesize+1]...)
	}
}

func (w *encbuf) encodeString(b []byte) {
	if len(b) == 1 && b[0] <= 0x7F {
		// fits single byte, no string header
		w.str = append(w.str, b[0])
	} else {
		w.encodeStringHeader(len(b))
		w.str = append(w.str, b...)
	}
}

func (w *encbuf) list() *listhead {
	lh := &listhead{offset: len(w.str), size: w.lhsize}
	w.lheads = append(w.lheads, lh)
	return lh
}

func (w *encbuf) listEnd(lh *listhead) {
	lh.size = w.size() - lh.offset - lh.size
	if lh.size < 56 {
		w.lhsize += 1 // length encoded into kind tag
	} else {
		w.lhsize += 1 + intsize(uint64(lh.size))
	}
}

func (w *encbuf) size() int {
	return len(w.str) + w.lhsize
}

func (w *encbuf) toBytes() []byte {
	out := make([]byte, w.size())
	strpos := 0
	pos := 0
	for _, head := range w.lheads {
		// write string data before header
		n := copy(out[pos:], w.str[strpos:head.offset])
		pos += n
		strpos += n
		// write the header
		enc := head.encode(out[pos:]) //把当前数据类型对应的头部信息插入（后续解析需要）
		pos += len(enc)
	}
	// 最后一个数据没有被头部索引标示，作为最后一个，它可以直接读取和保存
	// 因此在这里需要单独保存最后一部分数据到最终到byte[]中
	copy(out[pos:], w.str[strpos:])
	return out
}

func (w *encbuf) toWriter(out io.Writer) (err error) {
	strpos := 0
	for _, head := range w.lheads {
		// write string data before header
		if head.offset-strpos > 0 {
			n, err := out.Write(w.str[strpos:head.offset])
			strpos += n
			if err != nil {
				return err
			}
		}
		// write the header
		enc := head.encode(w.sizebuf)
		if _, err = out.Write(enc); err != nil {
			return err
		}
	}
	if strpos < len(w.str) {
		// write string data after the last list header
		_, err = out.Write(w.str[strpos:])
	}
	return err
}

// encReader is the io.Reader returned by EncodeToReader.
// It releases its encbuf at EOF.
type encReader struct {
	buf    *encbuf // the buffer we're reading from. this is nil when we're at EOF.
	lhpos  int     // index of list header that we're reading
	strpos int     // current position in string buffer
	piece  []byte  // next piece to be read
}

func (r *encReader) Read(b []byte) (n int, err error) {
	for {
		if r.piece = r.next(); r.piece == nil {
			// Put the encode buffer back into the pool at EOF when it
			// is first encountered. Subsequent calls still return EOF
			// as the error but the buffer is no longer valid.
			if r.buf != nil {
				encbufPool.Put(r.buf)
				r.buf = nil
			}
			return n, io.EOF
		}
		nn := copy(b[n:], r.piece)
		n += nn
		if nn < len(r.piece) {
			// piece didn't fit, see you next time.
			r.piece = r.piece[nn:]
			return n, nil
		}
		r.piece = nil
	}
}

// next returns the next piece of data to be read.
// it returns nil at EOF.
func (r *encReader) next() []byte {
	switch {
	case r.buf == nil:
		return nil

	case r.piece != nil:
		// There is still data available for reading.
		return r.piece

	case r.lhpos < len(r.buf.lheads):
		// We're before the last list header.
		head := r.buf.lheads[r.lhpos]
		sizebefore := head.offset - r.strpos
		if sizebefore > 0 {
			// String data before header.
			p := r.buf.str[r.strpos:head.offset]
			r.strpos += sizebefore
			return p
		} else {
			r.lhpos++
			return head.encode(r.buf.sizebuf)
		}

	case r.strpos < len(r.buf.str):
		// String data at the end, after all list headers.
		p := r.buf.str[r.strpos:]
		r.strpos = len(r.buf.str)
		return p

	default:
		return nil
	}
}

var (

	encoderInterface = reflect.TypeOf(new(Encoder)).Elem()
	big0             = big.NewInt(0)
)

// makeWriter creates a writer function for the given type.

/**
//编码入口
// 只是返回了连接后的新字串，写入
// writer为通用函数，数据类型一致，均可等同于该类型函数，
// go返回的不同方法名的理解：
// 方法名对应的与type write func(x,x)参数类型一致，表示为同一函数类型，
// 返回方法名，说明参数值默认为当前最近的值，就是那个val 和 w
 */
func makeWriter(typ reflect.Type, ts tags) (writer, error) {
	kind := typ.Kind()
	switch {
	case typ == rawValueType:
		return writeRawValue, nil //若为字节数组,直接写入
	case typ.Implements(encoderInterface):
		return writeEncoder, nil //若为自定义数据类型，实现了自定义编码接口
	case kind != reflect.Ptr && reflect.PtrTo(typ).Implements(encoderInterface):
		return writeEncoderNoPtr, nil  //若为数据类型转换成指针类型后，若该指针实现了自定义编码接口，则在此读取
	case kind == reflect.Interface:
		return writeInterface, nil    //若为go的一种数据类型Interface，需要自行实现，该串会被直接写入，在该方法返回空
	case typ.AssignableTo(reflect.PtrTo(bigInt)):
		return writeBigIntPtr, nil  //若为指针对应的大整型，一个数据，指针指向的是这个大整型的第一位，若为0，则说明这个大整型为0
	case typ.AssignableTo(bigInt):
		return writeBigIntNoPtr, nil //若为对应的大整型，一个数据
	case isUint(kind):
		return writeUint, nil  //若为无符号整型，一个数据
	case kind == reflect.Bool:
		return writeBool, nil
	case kind == reflect.String:
		return writeString, nil
	case kind == reflect.Slice && isByte(typ.Elem()):
		return writeBytes, nil
	case kind == reflect.Array && isByte(typ.Elem()):
		return writeByteArray, nil
	case kind == reflect.Slice || kind == reflect.Array:
		return makeSliceWriter(typ, ts) //数组类型，切片
	case kind == reflect.Struct:
		return makeStructWriter(typ)
	case kind == reflect.Ptr:
		return makePtrWriter(typ)
	default:
		return nil, fmt.Errorf("rlp: type %v is not RLP-serializable", typ)
	}
}

func isByte(typ reflect.Type) bool {
	return typ.Kind() == reflect.Uint8 && !typ.Implements(encoderInterface)
}

func writeRawValue(val reflect.Value, w *encbuf) error {
	w.str = append(w.str, val.Bytes()...)
	return nil
}

func writeUint(val reflect.Value, w *encbuf) error {
	i := val.Uint()
	if i == 0 {
		w.str = append(w.str, 0x80)  //默认加入128
	} else if i < 128 {
		// fits single byte
		w.str = append(w.str, byte(i))  //小于128,直接存入
	} else {
		// TODO: encode int to w.str directly
		s := putint(w.sizebuf[1:], i)
		w.sizebuf[0] = 0x80 + byte(s)
		w.str = append(w.str, w.sizebuf[:s+1]...)
	}
	return nil
}

func writeBool(val reflect.Value, w *encbuf) error {
	if val.Bool() {
		w.str = append(w.str, 0x01)
	} else {
		w.str = append(w.str, 0x80)
	}
	return nil
}

func writeBigIntPtr(val reflect.Value, w *encbuf) error {
	ptr := val.Interface().(*big.Int)
	if ptr == nil {
		w.str = append(w.str, 0x80)
		return nil
	}
	return writeBigInt(ptr, w)
}

func writeBigIntNoPtr(val reflect.Value, w *encbuf) error {
	i := val.Interface().(big.Int)
	return writeBigInt(&i, w)
}

func writeBigInt(i *big.Int, w *encbuf) error {
	if cmp := i.Cmp(big0); cmp == -1 {
		return fmt.Errorf("rlp: cannot encode negative *big.Int")
	} else if cmp == 0 {   //指针对应的大整型，一个数据，指针指向的是这个大整型的第一位，若为0，则说明这个大整型为0
		w.str = append(w.str, 0x80)  //按规则，128输入
	} else {
		w.encodeString(i.Bytes())
	}
	return nil
}

func writeBytes(val reflect.Value, w *encbuf) error {
	w.encodeString(val.Bytes())
	return nil
}

func writeByteArray(val reflect.Value, w *encbuf) error {
	if !val.CanAddr() {
		// Slice requires the value to be addressable.
		// Make it addressable by copying.
		copy := reflect.New(val.Type()).Elem()
		copy.Set(val)
		val = copy
	}
	size := val.Len()
	slice := val.Slice(0, size).Bytes()
	w.encodeString(slice)
	return nil
}

func writeString(val reflect.Value, w *encbuf) error {
	s := val.String()
	if len(s) == 1 && s[0] <= 0x7f {
		// fits single byte, no string header
		w.str = append(w.str, s[0])
	} else {
		w.encodeStringHeader(len(s))
		w.str = append(w.str, s...)
	}
	return nil
}

func writeEncoder(val reflect.Value, w *encbuf) error {
	return val.Interface().(Encoder).EncodeRLP(w)
}

// writeEncoderNoPtr handles non-pointer values that implement Encoder
// with a pointer receiver.
func writeEncoderNoPtr(val reflect.Value, w *encbuf) error {
	if !val.CanAddr() {
		// We can't get the address. It would be possible to make the
		// value addressable by creating a shallow copy, but this
		// creates other problems so we're not doing it (yet).
		//
		// package json simply doesn't call MarshalJSON for cases like
		// this, but encodes the value as if it didn't implement the
		// interface. We don't want to handle it that way.
		return fmt.Errorf("rlp: game over: unadressable value of type %v, EncodeRLP is pointer method", val.Type())
	}
	return val.Addr().Interface().(Encoder).EncodeRLP(w)
}

//数据类型是接口，将其编码写入
//Interface是go中的一种数据类型，需要手动实现method
func writeInterface(val reflect.Value, w *encbuf) error {
	if val.IsNil() {
		// Write empty list. This is consistent with the previous RLP
		// encoder that we had and should therefore avoid any
		// problems.
		w.str = append(w.str, 0xC0)  //看作结构体，字节长度小于56，则追加一个192
		return nil
	}
	eval := val.Elem()
	ti, err := cachedTypeInfo(eval.Type(), tags{})
	if err != nil {
		return err
	}
	return ti.writer(eval, w)
}

func makeSliceWriter(typ reflect.Type, ts tags) (writer, error) {
	etypeinfo, err := cachedTypeInfo1(typ.Elem(), tags{})
	if err != nil {
		return nil, err
	}
	writer := func(val reflect.Value, w *encbuf) error {
		if !ts.tail {
			defer w.listEnd(w.list())
		}
		vlen := val.Len()
		for i := 0; i < vlen; i++ {
			if err := etypeinfo.writer(val.Index(i), w); err != nil {
				return err
			}
		}
		return nil
	}
	return writer, nil
}

func makeStructWriter(typ reflect.Type) (writer, error) {
	fields, err := structFields(typ)
	if err != nil {
		return nil, err
	}
	writer := func(val reflect.Value, w *encbuf) error {
		lh := w.list()
		for _, f := range fields {
			if err := f.info.writer(val.Field(f.index), w); err != nil {
				return err
			}
		}
		w.listEnd(lh)
		return nil
	}
	return writer, nil
}

func makePtrWriter(typ reflect.Type) (writer, error) {
	etypeinfo, err := cachedTypeInfo1(typ.Elem(), tags{})
	if err != nil {
		return nil, err
	}

	// determine nil pointer handler
	var nilfunc func(*encbuf) error
	kind := typ.Elem().Kind()
	switch {
	case kind == reflect.Array && isByte(typ.Elem().Elem()):
		nilfunc = func(w *encbuf) error {
			w.str = append(w.str, 0x80)
			return nil
		}
	case kind == reflect.Struct || kind == reflect.Array:
		nilfunc = func(w *encbuf) error {
			// encoding the zero value of a struct/array could trigger
			// infinite recursion, avoid that.
			w.listEnd(w.list())
			return nil
		}
	default:
		zero := reflect.Zero(typ.Elem())
		nilfunc = func(w *encbuf) error {
			return etypeinfo.writer(zero, w)
		}
	}

	writer := func(val reflect.Value, w *encbuf) error {
		if val.IsNil() {
			return nilfunc(w)
		} else {
			return etypeinfo.writer(val.Elem(), w)
		}
	}
	return writer, err
}

// putint writes i to the beginning of b in big endian byte
// order, using the least number of bytes needed to represent i.
func putint(b []byte, i uint64) (size int) {
	switch {
	case i < (1 << 8):
		b[0] = byte(i)
		return 1
	case i < (1 << 16):
		b[0] = byte(i >> 8)
		b[1] = byte(i)
		return 2
	case i < (1 << 24):
		b[0] = byte(i >> 16)
		b[1] = byte(i >> 8)
		b[2] = byte(i)
		return 3
	case i < (1 << 32):
		b[0] = byte(i >> 24)
		b[1] = byte(i >> 16)
		b[2] = byte(i >> 8)
		b[3] = byte(i)
		return 4
	case i < (1 << 40):
		b[0] = byte(i >> 32)
		b[1] = byte(i >> 24)
		b[2] = byte(i >> 16)
		b[3] = byte(i >> 8)
		b[4] = byte(i)
		return 5
	case i < (1 << 48):
		b[0] = byte(i >> 40)
		b[1] = byte(i >> 32)
		b[2] = byte(i >> 24)
		b[3] = byte(i >> 16)
		b[4] = byte(i >> 8)
		b[5] = byte(i)
		return 6
	case i < (1 << 56):
		b[0] = byte(i >> 48)
		b[1] = byte(i >> 40)
		b[2] = byte(i >> 32)
		b[3] = byte(i >> 24)
		b[4] = byte(i >> 16)
		b[5] = byte(i >> 8)
		b[6] = byte(i)
		return 7
	default:
		b[0] = byte(i >> 56)
		b[1] = byte(i >> 48)
		b[2] = byte(i >> 40)
		b[3] = byte(i >> 32)
		b[4] = byte(i >> 24)
		b[5] = byte(i >> 16)
		b[6] = byte(i >> 8)
		b[7] = byte(i)
		return 8
	}
}

// intsize computes the minimum number of bytes required to store i.
func intsize(i uint64) (size int) {
	for size = 1; ; size++ {
		if i >>= 8; i == 0 {
			return size
		}
	}
}
