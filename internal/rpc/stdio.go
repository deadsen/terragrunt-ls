package rpc

import "io"

type stdio struct {
	io.Reader
	io.Writer
}

func NewStdio(r io.Reader, w io.Writer) io.ReadWriteCloser {
	return &stdio{Reader: r, Writer: w}
}

func (*stdio) Close() error { return nil }
