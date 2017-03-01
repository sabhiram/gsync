// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package gsync

import (
	"context"
	"crypto/md5"
	"fmt"
	"hash"
	"io"

	"github.com/pkg/errors"
)

// Checksums reads data blocks from reader and pipes out block checksums on the
// returning channel, closing it when done reading or when the context is cancelled.
// This function does not block and returns immediately. The caller must make sure the concrete
// reader instance is not nil or this function will panic.
func Checksums(ctx context.Context, r io.Reader, shash hash.Hash) (<-chan BlockChecksum, error) {
	var index uint64
	buffer := make([]byte, DefaultBlockSize)
	c := make(chan BlockChecksum)

	if r == nil {
		close(c)
		return nil, errors.New("gsync: reader required")
	}

	if shash == nil {
		shash = md5.New()
	}

	go func() {
		defer close(c)

		for {
			// Allow for cancellation
			select {
			case <-ctx.Done():
				c <- BlockChecksum{
					Index: index,
					Error: ctx.Err(),
				}
				return
			default:
				// break out of the select block and continue reading
				break
			}

			n, err := r.Read(buffer)
			if err == io.EOF {
				break
			}

			if err != nil {
				c <- BlockChecksum{
					Index: index,
					Error: errors.Wrapf(err, "failed reading block"),
				}
				index++
				// let the caller decide whether to interrupt the process or not.
				continue
			}
			shash.Reset()

			block := buffer[:n]
			weak := rollingHash(block)
			strong := shash.Sum(block)

			c <- BlockChecksum{
				Index:  index,
				Weak:   weak,
				Strong: strong,
			}
			index++
		}
	}()

	return c, nil
}

// Apply reconstructs a file given a set of operations. The caller must close the ops channel or the context when done or there will be a deadlock.
func Apply(ctx context.Context, dst io.Writer, cache io.ReaderAt, ops <-chan BlockOperation) error {
	for o := range ops {
		// Allows for cancellation.
		select {
		case <-ctx.Done():
			return errors.Wrapf(ctx.Err(), "failed applying block operations")
		default:
			// break out of the select block and continue reading ops
			break
		}

		if o.Error != nil {
			return errors.Wrapf(o.Error, "failed applying operation")
		}

		var block []byte
		index := int64(o.Index)

		if len(o.Data) > 0 {
			block = o.Data
		} else {
			buffer := make([]byte, DefaultBlockSize)
			n, err := cache.ReadAt(buffer, (index * DefaultBlockSize))
			if err != nil && err != io.EOF {
				return errors.Wrapf(err, "failed reading cached block")
			}

			if err != nil {
				fmt.Printf("warn: %#v", err)
			}

			block = buffer[:n]
		}

		//fmt.Printf("\napply: %s\n", string(o.Data[:]))
		_, err := dst.Write(block)
		if err != nil {
			return errors.Wrapf(err, "failed writing block to destination")
		}
	}
	return nil
}
