// SPDX-License-Identifier: MIT

package multilogs

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"go.cryptoscope.co/librarian"
	"go.cryptoscope.co/margaret"
	"go.cryptoscope.co/margaret/multilog"
	kitlog "go.mindeco.de/log"
	"go.mindeco.de/log/level"

	"go.cryptoscope.co/ssb"
	"go.cryptoscope.co/ssb/internal/storedrefs"
	"go.cryptoscope.co/ssb/message/multimsg"
	"go.cryptoscope.co/ssb/private/box"
	"go.cryptoscope.co/ssb/repo"
	gabbygrove "go.mindeco.de/ssb-gabbygrove"
	refs "go.mindeco.de/ssb-refs"
)

const IndexNamePrivates = "privates"

/* deprecated
// not strictly a multilog but allows multiple keys and gives us the good resumption
func NewPrivateRead(log kitlog.Logger, kps ...*ssb.KeyPair) *Private {
	return &Private{
		logger:   log,
		keyPairs: kps,
		boxer:    box.NewBoxer(nil),
	}
}
*/

type Private struct {
	logger kitlog.Logger

	keyPairs []ssb.KeyPair
	boxer    *box.Boxer
}

// OpenRoaring uses roaring bitmaps with a slim key-value store backend
func (pr Private) OpenRoaring(r repo.Interface) (multilog.MultiLog, librarian.SinkIndex, error) {
	return repo.OpenMultiLog(r, IndexNamePrivates, pr.update)
}

// OpenBadger uses a pretty memory hungry but battle-tested backend
func (pr Private) OpenBadger(r repo.Interface) (multilog.MultiLog, librarian.SinkIndex, error) {
	return repo.OpenBadgerMultiLog(r, IndexNamePrivates, pr.update)
}

func (pr Private) update(ctx context.Context, seq margaret.Seq, val interface{}, mlog multilog.MultiLog) error {
	if nulled, ok := val.(error); ok {
		if margaret.IsErrNulled(nulled) {
			return nil
		}
		return nulled
	}

	msg, ok := val.(refs.Message)
	if !ok {
		err := fmt.Errorf("private/readidx: error casting message. got type %T", val)
		return err
	}

	var boxedContent []byte
	switch msg.Author().Algo() {
	case refs.RefAlgoFeedSSB1:
		input := msg.ContentBytes()
		if !(input[0] == '"' && input[len(input)-1] == '"') {
			return nil // not a json string
		}
		b64data := bytes.TrimSuffix(input[1:], []byte(".box\""))
		boxedData := make([]byte, len(b64data))
		n, err := base64.StdEncoding.Decode(boxedData, b64data)
		if err != nil {
			err = fmt.Errorf("private/readidx: invalid b64 encoding: %w", err)
			level.Debug(pr.logger).Log("msg", "unboxLog b64 decode failed", "err", err)
			return nil
		}
		boxedContent = boxedData[:n]

	case refs.RefAlgoFeedGabby:
		mm, ok := val.(multimsg.MultiMessage)
		if !ok {
			mmPtr, ok := val.(*multimsg.MultiMessage)
			if !ok {
				err := fmt.Errorf("private/readidx: error casting message. got type %T", val)
				return err
			}
			mm = *mmPtr
		}
		tr, ok := mm.AsGabby()
		if !ok {
			err := errors.New("private/readidx: error getting gabby msg")
			return err
		}
		evt, err := tr.UnmarshaledEvent()
		if err != nil {
			return fmt.Errorf("private/readidx: error unpacking event from stored message: %w", err)
		}
		if evt.Content.Type != gabbygrove.ContentTypeArbitrary {
			return nil
		}
		boxedContent = bytes.TrimPrefix(tr.Content, []byte("box1:"))

	default:
		err := fmt.Errorf("private/readidx: unknown feed type: %s", msg.Author().Algo)
		level.Warn(pr.logger).Log("msg", "unahndled type", "err", err)
		return err
	}

	for _, kp := range pr.keyPairs {
		if _, err := pr.boxer.Decrypt(kp, boxedContent); err != nil {
			continue
		}
		userPrivs, err := mlog.Get(storedrefs.Feed(kp.Id))
		if err != nil {
			return fmt.Errorf("private/readidx: error opening priv sublog for %s: %w", kp.Id.Ref(), err)
		}
		_, err = userPrivs.Append(seq.Seq())
		if err != nil {
			return fmt.Errorf("private/readidx: error appending PM for %s: %w", kp.Id.Ref(), err)
		}
	}
	return nil
}
