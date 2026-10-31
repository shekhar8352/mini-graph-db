package server

import (
	"google.golang.org/protobuf/proto"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/exec"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

type sender func(*graphdbv1.ExecuteResponse) error

func sendResult(send sender, index int32, res *exec.Result, txnID string, maxRows, maxBytes int) error {
	if res == nil {
		res = exec.Rows(nil, nil)
	}
	if maxRows < 1 {
		maxRows = 1
	}
	if len(res.Columns) > 0 {
		msg := &graphdbv1.ExecuteResponse{
			StatementIndex: index,
			Part: &graphdbv1.ExecuteResponse_Columns{Columns: &graphdbv1.Columns{
				Names: append([]string(nil), res.Columns...),
			}},
		}
		if err := sendChecked(send, msg, maxBytes); err != nil {
			return err
		}
	}
	for _, note := range res.Notifications {
		msg := &graphdbv1.ExecuteResponse{
			StatementIndex: index,
			Part: &graphdbv1.ExecuteResponse_Notification{Notification: &graphdbv1.Notification{
				Code: note.Code, Message: note.Message,
			}},
		}
		if err := sendChecked(send, msg, maxBytes); err != nil {
			return err
		}
	}
	batch := &graphdbv1.Rows{}
	flush := func() error {
		if len(batch.Rows) == 0 {
			return nil
		}
		msg := &graphdbv1.ExecuteResponse{
			StatementIndex: index,
			Part:           &graphdbv1.ExecuteResponse_Rows{Rows: batch},
		}
		if err := sendChecked(send, msg, maxBytes); err != nil {
			return err
		}
		batch = &graphdbv1.Rows{}
		return nil
	}
	for {
		row, ok := res.Next()
		if !ok {
			break
		}
		pr := &graphdbv1.Row{Values: make([]*graphdbv1.Value, len(row))}
		for i, cell := range row {
			pv, err := toProto(cell)
			if err != nil {
				return err
			}
			pr.Values[i] = pv
		}
		trial := &graphdbv1.ExecuteResponse{
			StatementIndex: index,
			Part:           &graphdbv1.ExecuteResponse_Rows{Rows: &graphdbv1.Rows{Rows: []*graphdbv1.Row{pr}}},
		}
		if proto.Size(trial) > maxBytes {
			return gerr.New(gerr.ResourceExhausted, "result row exceeds max_message_bytes")
		}
		if len(batch.Rows) >= maxRows {
			if err := flush(); err != nil {
				return err
			}
		}
		next := &graphdbv1.ExecuteResponse{
			StatementIndex: index,
			Part: &graphdbv1.ExecuteResponse_Rows{Rows: &graphdbv1.Rows{
				Rows: append(append([]*graphdbv1.Row{}, batch.Rows...), pr),
			}},
		}
		if len(batch.Rows) > 0 && proto.Size(next) > maxBytes {
			if err := flush(); err != nil {
				return err
			}
		}
		batch.Rows = append(batch.Rows, pr)
	}
	if err := flush(); err != nil {
		return err
	}
	sum := &graphdbv1.Summary{
		NodesCreated:       int64(res.Summary.NodesCreated),
		NodesDeleted:       int64(res.Summary.NodesDeleted),
		EdgesCreated:       int64(res.Summary.EdgesCreated),
		EdgesDeleted:       int64(res.Summary.EdgesDeleted),
		PropertiesSet:      int64(res.Summary.PropertiesSet),
		LabelsAdded:        int64(res.Summary.LabelsAdded),
		LabelsRemoved:      int64(res.Summary.LabelsRemoved),
		IndexesAdded:       int64(res.Summary.IndexesAdded),
		IndexesRemoved:     int64(res.Summary.IndexesRemoved),
		ConstraintsAdded:   int64(res.Summary.ConstraintsAdded),
		ConstraintsRemoved: int64(res.Summary.ConstraintsRemoved),
		TxnId:              txnID,
	}
	for _, op := range res.Profile {
		sum.Profile = append(sum.Profile, &graphdbv1.Operator{
			Text:      op.Text,
			Depth:     int32(op.Depth),
			Rows:      op.Rows,
			DbHits:    op.DBHits,
			TimeNanos: op.Time.Nanoseconds(),
		})
	}
	return sendChecked(send, &graphdbv1.ExecuteResponse{
		StatementIndex: index,
		Part:           &graphdbv1.ExecuteResponse_Summary{Summary: sum},
	}, maxBytes)
}

func sendChecked(send sender, msg *graphdbv1.ExecuteResponse, maxBytes int) error {
	if proto.Size(msg) > maxBytes {
		return gerr.New(gerr.ResourceExhausted, "result message exceeds max_message_bytes")
	}
	return send(msg)
}
