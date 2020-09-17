// SPDX-License-Identifier: MIT

package transform

import (
	"context"
	"encoding/json"

	"github.com/cryptix/go/encodedTime"
	"github.com/pkg/errors"
	"go.cryptoscope.co/luigi"
	"go.cryptoscope.co/luigi/mfr"
	"go.cryptoscope.co/margaret"
	"go.cryptoscope.co/ssb/message/multimsg"

	"go.cryptoscope.co/ssb"
)

// NewKeyValueWrapper turns a value into a key-value message.
// If keyWrap is true, it returns the JSON of a ssb.KeyValueRaw value.
func NewKeyValueWrapper(output luigi.Sink, keyWrap bool) luigi.Sink {

	noNulled := mfr.FilterFunc(func(ctx context.Context, v interface{}) (bool, error) {
		if err, ok := v.(error); ok {
			if margaret.IsErrNulled(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})

	mapToKV := mfr.SinkMap(output, func(ctx context.Context, v interface{}) (interface{}, error) {
		var seqWrap margaret.SeqWrapper

		var abs ssb.Message
		switch tv := v.(type) {
		case ssb.Message:
			abs = tv
		case margaret.SeqWrapper:
			seqWrap = tv

			sv := tv.Value()
			var ok bool
			abs, ok = sv.(ssb.Message)
			if !ok {
				return nil, errors.Errorf("kvwrap: wrong message type in seqWrapper - got %T", sv)
			}
		}

		if !keyWrap {
			// skip re-encoding in some cases
			if mm, ok := abs.(*multimsg.MultiMessage); ok {
				leg, ok := mm.AsLegacy()
				if ok {
					return json.RawMessage(leg.Raw_), nil
				}
			}
			if mm, ok := abs.(multimsg.MultiMessage); ok {
				leg, ok := mm.AsLegacy()
				if ok {
					return json.RawMessage(leg.Raw_), nil
				}
			}

			return json.RawMessage(abs.ValueContentJSON()), nil
		}

		var kv ssb.KeyValueRaw
		kv.Key_ = abs.Key()
		kv.Value = *abs.ValueContent()
		kv.Timestamp = encodedTime.Millisecs(abs.Received())

		if seqWrap == nil {
			kvMsg, err := json.Marshal(kv)
			if err != nil {
				return nil, errors.Wrapf(err, "kvwrap: failed to k:v map message")
			}
			return json.RawMessage(kvMsg), nil
		}

		type sewWrapped struct {
			Value interface{} `json:"value"`
			Seq   int64       `json:"seq"`
		}

		sw := sewWrapped{
			Value: kv,
			Seq:   seqWrap.Seq().Seq(),
		}
		kvMsg, err := json.Marshal(sw)
		if err != nil {
			return nil, errors.Wrapf(err, "kvwrap: failed to k:v map message")
		}
		return json.RawMessage(kvMsg), nil
	})

	return mfr.SinkFilter(mapToKV, noNulled)
}
