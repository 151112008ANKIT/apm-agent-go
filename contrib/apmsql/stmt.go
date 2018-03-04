package apmsql

import (
	"context"
	"database/sql/driver"

	"github.com/elastic/apm-agent-go/model"
	"github.com/elastic/apm-agent-go/trace"
)

func newStmt(in driver.Stmt, conn *conn, query string) driver.Stmt {
	stmt := &stmt{
		Stmt:        in,
		conn:        conn,
		signature:   conn.driver.querySignature(query),
		spanContext: conn.spanContext(query),
	}
	stmt.columnConverter, _ = in.(driver.ColumnConverter)
	stmt.namedValueChecker, _ = in.(driver.NamedValueChecker)
	stmt.stmtExecContext, _ = in.(driver.StmtExecContext)
	stmt.stmtQueryContext, _ = in.(driver.StmtQueryContext)
	return stmt
}

type stmt struct {
	driver.Stmt
	conn        *conn
	signature   string
	spanContext *model.SpanContext

	columnConverter   driver.ColumnConverter
	namedValueChecker driver.NamedValueChecker
	stmtExecContext   driver.StmtExecContext
	stmtQueryContext  driver.StmtQueryContext
}

func (s *stmt) finishSpan(ctx context.Context, span *trace.Span, resultError error) {
	span.Context = s.spanContext
	s.conn.finishSpan(ctx, span, "", resultError)
}

func (s *stmt) ColumnConverter(idx int) driver.ValueConverter {
	if s.columnConverter != nil {
		return s.columnConverter.ColumnConverter(idx)
	}
	return driver.DefaultParameterConverter
}

func (s *stmt) CheckNamedValue(nv *driver.NamedValue) error {
	if s.namedValueChecker != nil {
		return s.namedValueChecker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (_ driver.Result, resultError error) {
	span, ctx := trace.StartSpan(ctx, s.signature, s.conn.driver.spanType("exec"))
	if span != nil {
		defer s.finishSpan(ctx, span, resultError)
	}
	if s.stmtExecContext != nil {
		return s.stmtExecContext.ExecContext(ctx, args)
	}
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	select {
	default:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Exec(dargs)
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (_ driver.Rows, resultError error) {
	span, ctx := trace.StartSpan(ctx, s.signature, s.conn.driver.spanType("query"))
	if span != nil {
		defer s.finishSpan(ctx, span, resultError)
	}
	if s.stmtQueryContext != nil {
		return s.stmtQueryContext.QueryContext(ctx, args)
	}
	dargs, err := namedValueToValue(args)
	if err != nil {
		return nil, err
	}
	select {
	default:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.Query(dargs)
}
