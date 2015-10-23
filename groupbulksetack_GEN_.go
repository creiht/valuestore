package valuestore

import (
	"encoding/binary"
	"io"
	"sync/atomic"
)

// bsam: entries:n
// bsam entry: keyA:8, keyB:8, timestampbits:8

const _GROUP_BULK_SET_ACK_MSG_TYPE = 0xec3577cc6dbb75bb
const _GROUP_BULK_SET_ACK_MSG_ENTRY_LENGTH = 40

type groupBulkSetAckState struct {
	inMsgChan             chan *groupBulkSetAckMsg
	inFreeMsgChan         chan *groupBulkSetAckMsg
	outFreeMsgChan        chan *groupBulkSetAckMsg
	inBulkSetAckDoneChans []chan struct{}
}

type groupBulkSetAckMsg struct {
	vs   *DefaultGroupStore
	body []byte
}

func (vs *DefaultGroupStore) bulkSetAckConfig(cfg *GroupStoreConfig) {
	if vs.msgRing != nil {
		vs.msgRing.SetMsgHandler(_GROUP_BULK_SET_ACK_MSG_TYPE, vs.newInBulkSetAckMsg)
		vs.bulkSetAckState.inMsgChan = make(chan *groupBulkSetAckMsg, cfg.InBulkSetAckMsgs)
		vs.bulkSetAckState.inFreeMsgChan = make(chan *groupBulkSetAckMsg, cfg.InBulkSetAckMsgs)
		for i := 0; i < cap(vs.bulkSetAckState.inFreeMsgChan); i++ {
			vs.bulkSetAckState.inFreeMsgChan <- &groupBulkSetAckMsg{
				vs:   vs,
				body: make([]byte, cfg.BulkSetAckMsgCap),
			}
		}
		vs.bulkSetAckState.inBulkSetAckDoneChans = make([]chan struct{}, cfg.InBulkSetAckWorkers)
		for i := 0; i < len(vs.bulkSetAckState.inBulkSetAckDoneChans); i++ {
			vs.bulkSetAckState.inBulkSetAckDoneChans[i] = make(chan struct{}, 1)
		}
		vs.bulkSetAckState.outFreeMsgChan = make(chan *groupBulkSetAckMsg, cfg.OutBulkSetAckMsgs)
		for i := 0; i < cap(vs.bulkSetAckState.outFreeMsgChan); i++ {
			vs.bulkSetAckState.outFreeMsgChan <- &groupBulkSetAckMsg{
				vs:   vs,
				body: make([]byte, cfg.BulkSetAckMsgCap),
			}
		}
	}
}

func (vs *DefaultGroupStore) bulkSetAckLaunch() {
	for i := 0; i < len(vs.bulkSetAckState.inBulkSetAckDoneChans); i++ {
		go vs.inBulkSetAck(vs.bulkSetAckState.inBulkSetAckDoneChans[i])
	}
}

// newInBulkSetAckMsg reads bulk-set-ack messages from the MsgRing and puts
// them on the inMsgChan for the inBulkSetAck workers to work on.
func (vs *DefaultGroupStore) newInBulkSetAckMsg(r io.Reader, l uint64) (uint64, error) {
	var bsam *groupBulkSetAckMsg
	select {
	case bsam = <-vs.bulkSetAckState.inFreeMsgChan:
	default:
		// If there isn't a free groupBulkSetAckMsg, just read and discard the
		// incoming bulk-set-ack message.
		left := l
		var sn int
		var err error
		for left > 0 {
			t := toss
			if left < uint64(len(t)) {
				t = t[:left]
			}
			sn, err = r.Read(t)
			left -= uint64(sn)
			if err != nil {
				atomic.AddInt32(&vs.inBulkSetAckInvalids, 1)
				return l - left, err
			}
		}
		atomic.AddInt32(&vs.inBulkSetAckDrops, 1)
		return l, nil
	}
	var n int
	var sn int
	var err error
	// TODO: Need to read up the actual msg cap and toss rest.
	if l > uint64(cap(bsam.body)) {
		bsam.body = make([]byte, l)
	}
	bsam.body = bsam.body[:l]
	n = 0
	for n != len(bsam.body) {
		sn, err = r.Read(bsam.body[n:])
		n += sn
		if err != nil {
			vs.bulkSetAckState.inFreeMsgChan <- bsam
			atomic.AddInt32(&vs.inBulkSetAckInvalids, 1)
			return uint64(n), err
		}
	}
	vs.bulkSetAckState.inMsgChan <- bsam
	atomic.AddInt32(&vs.inBulkSetAcks, 1)
	return l, nil
}

// inBulkSetAck actually processes incoming bulk-set-ack messages; there may be
// more than one of these workers.
func (vs *DefaultGroupStore) inBulkSetAck(doneChan chan struct{}) {
	for {
		bsam := <-vs.bulkSetAckState.inMsgChan
		if bsam == nil {
			break
		}
		ring := vs.msgRing.Ring()
		var rightwardPartitionShift uint64
		if ring != nil {
			rightwardPartitionShift = 64 - uint64(ring.PartitionBitCount())
		}
		b := bsam.body
		// div mul just ensures any trailing bytes are dropped
		l := len(b) / _GROUP_BULK_SET_ACK_MSG_ENTRY_LENGTH * _GROUP_BULK_SET_ACK_MSG_ENTRY_LENGTH
		for o := 0; o < l; o += _GROUP_BULK_SET_ACK_MSG_ENTRY_LENGTH {
			keyA := binary.BigEndian.Uint64(b[o:])
			if ring != nil && !ring.Responsible(uint32(keyA>>rightwardPartitionShift)) {
				atomic.AddInt32(&vs.inBulkSetAckWrites, 1)
				timestampbits := binary.BigEndian.Uint64(b[o+32:]) | _TSB_LOCAL_REMOVAL
				rtimestampbits, err := vs.write(keyA, binary.BigEndian.Uint64(b[o+8:]), binary.BigEndian.Uint64(b[o+16:]), binary.BigEndian.Uint64(b[o+24:]), timestampbits, nil, true)
				if err != nil {
					atomic.AddInt32(&vs.inBulkSetAckWriteErrors, 1)
				} else if rtimestampbits != timestampbits {
					atomic.AddInt32(&vs.inBulkSetAckWritesOverridden, 1)
				}
			}
		}
		vs.bulkSetAckState.inFreeMsgChan <- bsam
	}
	doneChan <- struct{}{}
}

// newOutBulkSetAckMsg gives an initialized groupBulkSetAckMsg for filling out
// and eventually sending using the MsgRing. The MsgRing (or someone else if
// the message doesn't end up with the MsgRing) will call
// groupBulkSetAckMsg.Free() eventually and the groupBulkSetAckMsg will be
// requeued for reuse later. There is a fixed number of outgoing
// groupBulkSetAckMsg instances that can exist at any given time, capping
// memory usage. Once the limit is reached, this method will block until a
// groupBulkSetAckMsg is available to return.
func (vs *DefaultGroupStore) newOutBulkSetAckMsg() *groupBulkSetAckMsg {
	bsam := <-vs.bulkSetAckState.outFreeMsgChan
	bsam.body = bsam.body[:0]
	return bsam
}

func (bsam *groupBulkSetAckMsg) MsgType() uint64 {
	return _GROUP_BULK_SET_ACK_MSG_TYPE
}

func (bsam *groupBulkSetAckMsg) MsgLength() uint64 {
	return uint64(len(bsam.body))
}

func (bsam *groupBulkSetAckMsg) WriteContent(w io.Writer) (uint64, error) {
	n, err := w.Write(bsam.body)
	return uint64(n), err
}

func (bsam *groupBulkSetAckMsg) Free() {
	bsam.vs.bulkSetAckState.outFreeMsgChan <- bsam
}

func (bsam *groupBulkSetAckMsg) add(keyA uint64, keyB uint64, nameKeyA uint64, nameKeyB uint64, timestampbits uint64) bool {
	o := len(bsam.body)
	if o+_GROUP_BULK_SET_ACK_MSG_ENTRY_LENGTH >= cap(bsam.body) {
		return false
	}
	bsam.body = bsam.body[:o+_GROUP_BULK_SET_ACK_MSG_ENTRY_LENGTH]

	binary.BigEndian.PutUint64(bsam.body[o:], keyA)
	binary.BigEndian.PutUint64(bsam.body[o+8:], keyB)
	binary.BigEndian.PutUint64(bsam.body[o+16:], nameKeyA)
	binary.BigEndian.PutUint64(bsam.body[o+24:], nameKeyB)
	binary.BigEndian.PutUint64(bsam.body[o+32:], timestampbits)

	return true
}