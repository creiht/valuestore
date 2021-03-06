package store

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestGroupValuesFileReading(t *testing.T) {
	store, _, err := NewGroupStore(lowMemGroupStoreConfig())
	if err != nil {
		t.Fatal("")
	}
	buf := &memBuf{buf: []byte("GROUPSTORE v0                   0123456789abcdef")}
	binary.BigEndian.PutUint32(buf.buf[28:], 65532)
	openReadSeeker := func(name string) (io.ReadSeeker, error) {
		return &memFile{buf: buf}, nil
	}
	fl, err := newGroupReadFile(store, 12345, openReadSeeker)
	if err != nil {
		t.Fatal("")
	}
	if fl == nil {
		t.Fatal("")
	}
	tsn := fl.timestampnano()
	if tsn != 12345 {
		t.Fatal(tsn)
	}
	ts, v, err := fl.read(1, 2, 0, 0, 0x300, _GROUP_FILE_HEADER_SIZE+4, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 0x300 {
		t.Fatal(ts)
	}
	if string(v) != "45678" {
		t.Fatal(string(v))
	}
	ts, v, err = fl.read(1, 2, 0, 0, 0x300|_TSB_DELETION, _GROUP_FILE_HEADER_SIZE+4, 5, nil)
	if err != ErrNotFound {
		t.Fatal(err)
	}
	if ts != 0x300|_TSB_DELETION {
		t.Fatal(ts)
	}
	if v != nil {
		t.Fatal(v)
	}
	ts, v, err = fl.read(1, 2, 0, 0, 0x300, _GROUP_FILE_HEADER_SIZE+4, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 0x300 {
		t.Fatal(ts)
	}
	if string(v) != "45678" {
		t.Fatal(string(v))
	}
	_, _, err = fl.read(1, 2, 0, 0, 0x300, _GROUP_FILE_HEADER_SIZE+12, 5, nil)
	if err != io.ErrUnexpectedEOF {
		t.Fatal(err)
	}
	ts, v, err = fl.read(1, 2, 0, 0, 0x300, _GROUP_FILE_HEADER_SIZE+4, 5, []byte("testing"))
	if err != nil {
		t.Fatal(err)
	}
	if ts != 0x300 {
		t.Fatal(ts)
	}
	if string(v) != "testing45678" {
		t.Fatal(string(v))
	}
	v = make([]byte, 0, 50)
	ts, v, err = fl.read(1, 2, 0, 0, 0x300, _GROUP_FILE_HEADER_SIZE+4, 5, v)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 0x300 {
		t.Fatal(ts)
	}
	if string(v) != "45678" {
		t.Fatal(string(v))
	}
	ts, v, err = fl.read(1, 2, 0, 0, 0x300, _GROUP_FILE_HEADER_SIZE+4, 5, v)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 0x300 {
		t.Fatal(ts)
	}
	if string(v) != "4567845678" {
		t.Fatal(string(v))
	}
}

func TestGroupValuesFileWritingEmpty(t *testing.T) {
	cfg := lowMemGroupStoreConfig()
	cfg.ChecksumInterval = 64*1024 - 4
	store, _, err := NewGroupStore(cfg)
	if err != nil {
		t.Fatal("")
	}
	buf := &memBuf{}
	createWriteCloser := func(name string) (io.WriteCloser, error) {
		return &memFile{buf: buf}, nil
	}
	openReadSeeker := func(name string) (io.ReadSeeker, error) {
		return &memFile{buf: buf}, nil
	}
	fl, err := createGroupReadWriteFile(store, createWriteCloser, openReadSeeker)
	if err != nil {
		t.Fatal("")
	}
	if fl == nil {
		t.Fatal("")
	}
	fl.close()
	bl := len(buf.buf)
	if bl != _GROUP_FILE_HEADER_SIZE+cfg.ChecksumInterval+4 {
		t.Fatal(bl)
	}
	if string(buf.buf[:28]) != "GROUPSTORE v0               " {
		t.Fatal(string(buf.buf[:28]))
	}
	if binary.BigEndian.Uint32(buf.buf[28:]) != store.checksumInterval {
		t.Fatal(binary.BigEndian.Uint32(buf.buf[28:]), store.checksumInterval)
	}
	if string(buf.buf[bl-8:]) != "TERM v0 " {
		t.Fatal(string(buf.buf[bl-8:]))
	}
}

func TestGroupValuesFileWritingEmpty2(t *testing.T) {
	cfg := lowMemGroupStoreConfig()
	cfg.ChecksumInterval = 64*1024 - 4
	store, _, err := NewGroupStore(cfg)
	if err != nil {
		t.Fatal("")
	}
	store.freeableMemBlockChans = make([]chan *groupMemBlock, 1)
	store.freeableMemBlockChans[0] = make(chan *groupMemBlock, 1)
	buf := &memBuf{}
	createWriteCloser := func(name string) (io.WriteCloser, error) {
		return &memFile{buf: buf}, nil
	}
	openReadSeeker := func(name string) (io.ReadSeeker, error) {
		return &memFile{buf: buf}, nil
	}
	fl, err := createGroupReadWriteFile(store, createWriteCloser, openReadSeeker)
	if err != nil {
		t.Fatal("")
	}
	if fl == nil {
		t.Fatal("")
	}
	memBlock := &groupMemBlock{values: []byte{}}
	memBlock.fileID = 123
	fl.write(memBlock)
	fl.close()
	if memBlock.fileID != fl.id {
		t.Fatal(memBlock.fileID, fl.id)
	}
	bl := len(buf.buf)
	if bl != _GROUP_FILE_HEADER_SIZE+cfg.ChecksumInterval+4 {
		t.Fatal(bl)
	}
	if string(buf.buf[:28]) != "GROUPSTORE v0               " {
		t.Fatal(string(buf.buf[:28]))
	}
	if binary.BigEndian.Uint32(buf.buf[28:]) != store.checksumInterval {
		t.Fatal(binary.BigEndian.Uint32(buf.buf[28:]), store.checksumInterval)
	}
	if string(buf.buf[bl-8:]) != "TERM v0 " {
		t.Fatal(string(buf.buf[bl-8:]))
	}
}

func TestGroupValuesFileWriting(t *testing.T) {
	cfg := lowMemGroupStoreConfig()
	cfg.ChecksumInterval = 64*1024 - 4
	store, _, err := NewGroupStore(cfg)
	if err != nil {
		t.Fatal("")
	}
	buf := &memBuf{}
	createWriteCloser := func(name string) (io.WriteCloser, error) {
		return &memFile{buf: buf}, nil
	}
	openReadSeeker := func(name string) (io.ReadSeeker, error) {
		return &memFile{buf: buf}, nil
	}
	fl, err := createGroupReadWriteFile(store, createWriteCloser, openReadSeeker)
	if err != nil {
		t.Fatal("")
	}
	if fl == nil {
		t.Fatal("")
	}
	values := make([]byte, 1234)
	copy(values, []byte("0123456789abcdef"))
	values[1233] = 1
	fl.write(&groupMemBlock{values: values})
	fl.close()
	bl := len(buf.buf)
	if bl != 1234+_GROUP_FILE_HEADER_SIZE+cfg.ChecksumInterval+4 {
		t.Fatal(bl)
	}
	if string(buf.buf[:28]) != "GROUPSTORE v0               " {
		t.Fatal(string(buf.buf[:28]))
	}
	if binary.BigEndian.Uint32(buf.buf[28:]) != store.checksumInterval {
		t.Fatal(binary.BigEndian.Uint32(buf.buf[28:]), store.checksumInterval)
	}
	if !bytes.Equal(buf.buf[_GROUP_FILE_HEADER_SIZE:bl-cfg.ChecksumInterval-4], values) {
		t.Fatal("")
	}
	if string(buf.buf[bl-_GROUP_FILE_TRAILER_SIZE:]) != "TERM v0 " {
		t.Fatal(string(buf.buf[bl-_GROUP_FILE_TRAILER_SIZE:]))
	}
}

func TestGroupValuesFileWritingMore(t *testing.T) {
	cfg := lowMemGroupStoreConfig()
	cfg.ChecksumInterval = 64*1024 - 4
	store, _, err := NewGroupStore(cfg)
	if err != nil {
		t.Fatal("")
	}
	buf := &memBuf{}
	createWriteCloser := func(name string) (io.WriteCloser, error) {
		return &memFile{buf: buf}, nil
	}
	openReadSeeker := func(name string) (io.ReadSeeker, error) {
		return &memFile{buf: buf}, nil
	}
	fl, err := createGroupReadWriteFile(store, createWriteCloser, openReadSeeker)
	if err != nil {
		t.Fatal("")
	}
	if fl == nil {
		t.Fatal("")
	}
	values := make([]byte, 123456)
	copy(values, []byte("0123456789abcdef"))
	values[1233] = 1
	fl.write(&groupMemBlock{values: values})
	fl.close()
	bl := len(buf.buf)
	dl := _GROUP_FILE_HEADER_SIZE + 123456 + cfg.ChecksumInterval
	el := dl + dl/cfg.ChecksumInterval*4
	if bl != el {
		t.Fatal(bl, el)
	}
	if string(buf.buf[:28]) != "GROUPSTORE v0               " {
		t.Fatal(string(buf.buf[:28]))
	}
	if binary.BigEndian.Uint32(buf.buf[28:]) != store.checksumInterval {
		t.Fatal(binary.BigEndian.Uint32(buf.buf[28:]), store.checksumInterval)
	}
	if string(buf.buf[bl-_GROUP_FILE_TRAILER_SIZE:]) != "TERM v0 " {
		t.Fatal(string(buf.buf[bl-_GROUP_FILE_TRAILER_SIZE:]))
	}
}

func TestGroupValuesFileWritingMultiple(t *testing.T) {
	cfg := lowMemGroupStoreConfig()
	cfg.ChecksumInterval = 64*1024 - 4
	store, _, err := NewGroupStore(cfg)
	if err != nil {
		t.Fatal("")
	}
	store.freeableMemBlockChans = make([]chan *groupMemBlock, 1)
	store.freeableMemBlockChans[0] = make(chan *groupMemBlock, 2)
	buf := &memBuf{}
	createWriteCloser := func(name string) (io.WriteCloser, error) {
		return &memFile{buf: buf}, nil
	}
	openReadSeeker := func(name string) (io.ReadSeeker, error) {
		return &memFile{buf: buf}, nil
	}
	fl, err := createGroupReadWriteFile(store, createWriteCloser, openReadSeeker)
	if err != nil {
		t.Fatal("")
	}
	if fl == nil {
		t.Fatal("")
	}
	values1 := make([]byte, 12345)
	copy(values1, []byte("0123456789abcdef"))
	memBlock1 := &groupMemBlock{values: values1}
	fl.write(memBlock1)
	values2 := make([]byte, 54321)
	copy(values2, []byte("fedcba9876543210"))
	memBlock2 := &groupMemBlock{values: values2}
	fl.write(memBlock2)
	fl.close()
	if memBlock1.fileID != fl.id {
		t.Fatal(memBlock1.fileID, fl.id)
	}
	if memBlock2.fileID != fl.id {
		t.Fatal(memBlock2.fileID, fl.id)
	}
	bl := len(buf.buf)
	dl := _GROUP_FILE_HEADER_SIZE + 12345 + 54321 + cfg.ChecksumInterval
	el := dl + dl/cfg.ChecksumInterval*4
	if bl != el {
		t.Fatal(bl, el)
	}
	if string(buf.buf[:28]) != "GROUPSTORE v0               " {
		t.Fatal(string(buf.buf[:28]))
	}
	if binary.BigEndian.Uint32(buf.buf[28:]) != store.checksumInterval {
		t.Fatal(binary.BigEndian.Uint32(buf.buf[28:]), store.checksumInterval)
	}
	if string(buf.buf[bl-_GROUP_FILE_TRAILER_SIZE:]) != "TERM v0 " {
		t.Fatal(string(buf.buf[bl-_GROUP_FILE_TRAILER_SIZE:]))
	}
}
