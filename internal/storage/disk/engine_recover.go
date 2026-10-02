package disk

import (
	"errors"
	"io"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
	"github.com/shekhar8352/mini-graph-db/internal/wal"
)

type replayTxn struct {
	pages [][]byte
}

func (e *Engine) recover() error {
	ckpt := e.file.CheckpointLSN()
	if ckpt > 0 && e.wal.End() < wal.LSN(ckpt) {
		return gerr.Newf(gerr.Corruption, "wal ends at lsn %d before checkpoint lsn %d", e.wal.End(), ckpt)
	}
	r, err := e.wal.Reader()
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := r.Seek(wal.LSN(ckpt)); err != nil {
		return err
	}
	pending := map[uint64]*replayTxn{}
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if rec.TxnID > e.nextTxn && rec.Type != wal.TypeCheckpoint {
			e.nextTxn = rec.TxnID
		}
		switch rec.Type {
		case wal.TypeTxnBegin, wal.TypeTxnAbort:
			if rec.Type == wal.TypeTxnAbort {
				delete(pending, rec.TxnID)
			}
		case wal.TypeCheckpoint, wal.TypeTreeInsert, wal.TypeTreeDelete:
			// Tree records are not emitted. A checkpoint marker has no pages.
		case wal.TypePageWrite:
			id, image, err := wal.DecodePageWrite(rec.Payload)
			if err != nil {
				return err
			}
			if len(image) < 8 || PageID(id) != pageIDOf(image) {
				return gerr.Newf(gerr.Corruption, "wal record %d page image does not match page %d", rec.LSN, id)
			}
			tx := pending[rec.TxnID]
			if tx == nil {
				tx = &replayTxn{}
				pending[rec.TxnID] = tx
			}
			tx.pages = append(tx.pages, image)
		case wal.TypeTxnCommit:
			tx := pending[rec.TxnID]
			delete(pending, rec.TxnID)
			if tx == nil {
				continue
			}
			for _, image := range tx.pages {
				if err := e.file.InstallImage(image); err != nil {
					return err
				}
			}
		default:
			return gerr.Newf(gerr.Corruption, "wal record %d has type %d", rec.LSN, rec.Type)
		}
	}
	return nil
}
