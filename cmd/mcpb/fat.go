package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
)

// machoFat combines thin Mach-O binaries into a universal ("fat") binary,
// equivalent to `lipo -create`, without needing Xcode tools.
func machoFat(thin ...[]byte) ([]byte, error) {
	const align = 14 // 2^14 = 16 KiB, what lipo uses for arm64
	type arch struct {
		cpu, sub  uint32
		off, size uint32
		data      []byte
	}
	archs := make([]arch, 0, len(thin))
	for _, b := range thin {
		f, err := macho.NewFile(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		archs = append(archs, arch{cpu: uint32(f.Cpu), sub: f.SubCpu, size: uint32(len(b)), data: b})
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	off := uint32(8 + 20*len(archs))
	for i := range archs {
		off = (off + (1<<align - 1)) &^ (1<<align - 1)
		archs[i].off = off
		off += archs[i].size
	}

	be := binary.BigEndian
	out := make([]byte, 0, off)
	out = be.AppendUint32(out, macho.MagicFat)
	out = be.AppendUint32(out, uint32(len(archs)))
	for _, a := range archs {
		for _, v := range []uint32{a.cpu, a.sub, a.off, a.size, align} {
			out = be.AppendUint32(out, v)
		}
	}
	for _, a := range archs {
		out = append(out, make([]byte, int(a.off)-len(out))...)
		out = append(out, a.data...)
	}
	return out, nil
}
