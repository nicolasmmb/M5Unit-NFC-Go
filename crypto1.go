package nfc

// Crypto1 is the MIFARE Classic stream cipher (48-bit LFSR).
type Crypto1 struct {
	st [48]uint8
}

var crypto1Taps = [...]int{4, 5, 6, 8, 12, 18, 20, 22, 23, 28, 30, 32, 33, 35, 37, 38, 42, 47}

func (c *Crypto1) init(key uint64) {
	for i := 0; i < 48; i++ {
		bi := i >> 3
		bj := i & 7
		rev := bi<<3 + (bj ^ 7)
		c.st[i] = uint8(key>>uint(rev)) & 1
	}
}

func fa(a, b, c, d uint8) uint8 {
	return ((a | b) ^ (a & d) ^ (c & ((a ^ b) | d))) & 1
}

func fb(a, b, c, d uint8) uint8 {
	return ((a & b) | c) ^ ((a^b)&(c|d))&1
}

func fc(a, b, c, d, e uint8) uint8 {
	return (a | ((b | e) & (d ^ e))) ^ ((a ^ (b & d)) & ((c ^ d) | (b & e)))
}

func (c *Crypto1) filter() uint8 {
	s := &c.st
	b5 := fb(s[6], s[4], s[2], s[0])
	a4 := fa(s[14], s[12], s[10], s[8])
	b3 := fb(s[22], s[20], s[18], s[16])
	b2 := fb(s[30], s[28], s[26], s[24])
	a1 := fa(s[38], s[36], s[34], s[32])
	return fc(a1, b2, b3, a4, b5) & 1
}

func (c *Crypto1) step() uint8 {
	out := c.st[47]
	var f uint8
	for _, i := range crypto1Taps {
		f ^= c.st[i]
	}
	for k := 47; k > 0; k-- {
		c.st[k] = c.st[k-1]
	}
	c.st[0] = f
	return out
}

func (c *Crypto1) stepWith(bitIn uint8, enc bool) uint8 {
	z := c.filter()
	c.step()
	ext := bitIn
	if enc {
		ext ^= z
	}
	c.st[0] ^= ext
	return z
}

func (c *Crypto1) step8(val uint8, enc bool) uint8 {
	var v uint8
	for i := 0; i < 8; i++ {
		t := c.stepWith((val>>uint(i))&1, enc)
		v |= t << uint(i)
	}
	return v
}

func (c *Crypto1) step32In(val uint32, enc bool) uint32 {
	var v uint32
	for i := 0; i < 32; i++ {
		t := c.stepWith(uint8(val>>uint(i^24)&1), enc)
		v |= uint32(t) << uint(24^i)
	}
	return v
}

func (c *Crypto1) inject(uid, nt uint32) uint32 {
	return c.step32In(uid^nt, false)
}

func (c *Crypto1) encryptStream(plain []byte) ([]byte, uint8) {
	var parity uint8
	for i := range plain {
		ks := c.step8(0, false)
		plain[i] ^= ks
		z := c.filter()
		parity |= ((z ^ oddParity8(plain[i]^ks)) & 1) << uint(i)
	}
	return plain, parity
}

func (c *Crypto1) encryptAB(nr, ar uint32) ([8]byte, uint8) {
	var buf [8]byte
	var parity uint8
	for i := 0; i < 4; i++ {
		v := uint8(nr >> uint((i^3)<<3))
		buf[i] = c.step8(v, false) ^ v
		z := c.filter()
		parity |= ((z ^ oddParity8(v)) & 1) << uint(i)
	}
	for pos := 4; pos < 8; pos++ {
		i := pos - 4
		v := uint8(ar >> uint(i<<3))
		ks := c.step8(0, false)
		buf[pos] = ks ^ v
		z := c.filter()
		parity |= ((z ^ oddParity8(v)) & 1) << uint(pos)
	}
	return buf, parity
}

func oddParity8(x uint8) uint8 {
	p := uint8(0)
	for x != 0 {
		p ^= x & 1
		x >>= 1
	}
	return p ^ 1
}

func appendParityTo(dst, enc []byte, parity uint8) int {
	outLen := (len(enc)*9 + 7) / 8
	if outLen > len(dst) {
		outLen = len(dst)
	}
	out := dst[:outLen]
	for i := range out {
		out[i] = 0
	}
	bitpos := 0
	for i, v := range enc {
		for k := 0; k < 8; k++ {
			if v>>uint(k)&1 != 0 {
				out[bitpos>>3] |= 1 << uint(bitpos&7)
			}
			bitpos++
		}
		if parity>>uint(i)&1 != 0 {
			out[bitpos>>3] |= 1 << uint(bitpos&7)
		}
		bitpos++
	}
	return outLen
}

func rev8(x uint8) uint8 {
	x = (x&0xF0)>>4 | (x&0x0F)<<4
	x = (x&0xCC)>>2 | (x&0x33)<<2
	x = (x&0xAA)>>1 | (x&0x55)<<1
	return x
}

func rev16(x uint16) uint16 {
	x = (x&0xFF00)>>8 | (x&0x00FF)<<8
	x = (x&0xF0F0)>>4 | (x&0x0F0F)<<4
	x = (x&0xCCCC)>>2 | (x&0x3333)<<2
	x = (x&0xAAAA)>>1 | (x&0x5555)<<1
	return x
}

func crc16A(data []byte) uint16 {
	crc := uint16(0xC6C6)
	for _, b := range data {
		crc ^= uint16(rev8(b)) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return rev16(crc)
}

func keyToU48(k [6]byte) uint64 {
	return uint64(k[0])<<40 | uint64(k[1])<<32 | uint64(k[2])<<24 |
		uint64(k[3])<<16 | uint64(k[4])<<8 | uint64(k[5])
}

func uidTailU32BE(uid []byte) uint32 {
	t := uid[len(uid)-4:]
	return uint32(t[0])<<24 | uint32(t[1])<<16 | uint32(t[2])<<8 | uint32(t[3])
}

func ntFromRB(rb []byte) uint32 {
	return uint32(rb[0])<<24 | uint32(rb[1])<<16 | uint32(rb[2])<<8 | uint32(rb[3])
}

func byteswap32(x uint32) uint32 {
	return x<<24 | (x&0xFF00)<<8 | (x>>8)&0xFF00 | x>>24
}

func suc23(nt uint32) (ar, suc3 uint32) {
	s := nt
	step := func() uint8 {
		out := uint8(s & 1)
		fb := (s >> 16) ^ (s >> 18) ^ (s >> 19) ^ (s>>21)&1
		s = s>>1 | fb<<31
		return out
	}
	next32 := func() uint32 {
		var v uint32
		for i := 0; i < 32; i++ {
			v |= uint32(step()) << uint(i)
		}
		return v
	}
	next32()
	next32()
	ar = next32()
	suc3 = next32()
	return
}
