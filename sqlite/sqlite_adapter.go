package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"strconv"

	"github.com/moleculer-go/moleculer/payload"

	"github.com/moleculer-go/moleculer"

	"github.com/moleculer-go/sqlite"
	"github.com/moleculer-go/sqlite/sqlitex"
	log "github.com/sirupsen/logrus"
)

type Column struct {
	Name string
	Type string
}

type Adapter struct {
	URI      string
	Flags    sqlite.OpenFlags
	PoolSize int
	Timeout  time.Duration
	Table    string
	Columns  []Column
	// ColName can be used to modify/translate column names
	// from what is passed in the params
	ColName func(string) string

	pool     *sqlitex.Pool
	log      *log.Entry
	settings map[string]interface{}

	fields  []string
	idField string
}

func (a *Adapter) Init(log *log.Entry, settings map[string]interface{}) {
	a.log = log
	a.settings = settings
	if a.Timeout == 0 {
		a.Timeout = time.Second * 2
	}
	if a.ColName == nil {
		a.ColName = func(value string) string {
			return value
		}
	}
	if a.PoolSize == 0 {
		a.PoolSize = 1
	}
	a.loadSettings(a.settings)
}

func (a *Adapter) Connect() error {
	pool, err := sqlitex.Open(a.URI, a.Flags, a.PoolSize)
	if err != nil {
		a.log.Error("Could not connect to SQLite - error: ", err)
		return errors.New(fmt.Sprint("Could not connect to SQLite - error: ", err))
	}
	a.pool = pool
	err = a.createTable()
	if err != nil {
		a.log.Error("Could not create table - error: ", err)
		return errors.New(fmt.Sprint("Could not create table - error: ", err))
	}
	return nil
}

func (a *Adapter) columnsDefinition() []string {
	columns := []string{a.idField + " INTEGER PRIMARY KEY AUTOINCREMENT"}
	for _, c := range a.Columns {
		def := c.Name
		if c.Type != "" {
			def = def + " " + c.Type
		}
		columns = append(columns, def)
	}
	return columns
}

func (a *Adapter) createTable() error {
	conn := a.getConn()
	if conn == nil {
		return noConnectionError().Error()
	}
	defer a.returnConn(conn)

	create := "CREATE TABLE " + a.Table + " (" + strings.Join(a.columnsDefinition(), ", ") + ");"
	a.log.Debug(create)

	err := sqlitex.ExecTransient(conn, create, nil)
	if err != nil {
		return err
	}
	a.log.Debug("table " + a.Table + " created !!!")
	return nil
}

func (a *Adapter) Disconnect() error {
	err := a.pool.Close()
	if err != nil {
		a.log.Error("Could not disconnect SQLite - error: ", err)
		return errors.New(fmt.Sprint("Could not disconnect SQLite - error: ", err))
	}
	return nil
}

func noConnectionError() moleculer.Payload {
	return payload.Error("No connection availble!. Did you call adapter.Connect() ?")
}

func (a *Adapter) returnConn(conn *sqlite.Conn) {
	a.pool.Put(conn)
}

func (a *Adapter) getConn() *sqlite.Conn {
	return a.pool.Get(nil)
}

func (a *Adapter) updatePairs(param moleculer.Payload) ([]string, []interface{}) {
	columns := []string{}
	values := []interface{}{}
	param.ForEach(func(key interface{}, value moleculer.Payload) bool {
		col, ok := key.(string)
		if !ok {
			a.log.Error("extractFields() key must be string! - key: ", key)
			return false
		}
		columns = append(columns, a.ColName(col)+" = ?")
		values = append(values, value.Value())
		return true
	})
	return columns, values
}

// extractFields will parse the payload and extract the column names,
// and value placeholders -> $name and the list of fields.
func (a *Adapter) insertFields(param moleculer.Payload) ([]string, []interface{}) {
	columns := []string{}
	values := []interface{}{}
	param.ForEach(func(key interface{}, value moleculer.Payload) bool {
		col, ok := key.(string)
		if !ok {
			a.log.Error("extractFields() key must be string! - key: ", key)
			return false
		}
		columns = append(columns, a.ColName(col))
		values = append(values, value.Value())
		return true
	})
	return columns, values
}

func (a *Adapter) populateStmt(stmt *sqlite.Stmt, param moleculer.Payload, fields []string) (err error) {
	param.ForEach(func(key interface{}, value moleculer.Payload) bool {
		field, ok := key.(string)
		if !ok {
			a.log.Error("populateStmt() key must be string! - key: ", key)
			err = errors.New(fmt.Sprint("populateStmt() key must be string! - key: ", key))
			return false
		}
		stmt.SetText("$"+field, value.String())
		return true
	})
	return err
}

func placeholders(c []string) []string {
	p := make([]string, len(c))
	for i, _ := range c {
		p[i] = "?"
	}
	return p
}

func (a *Adapter) loadSettings(settings map[string]interface{}) {
	idField, hasIdField := settings["idField"].(string)
	if !hasIdField {
		idField = "id"
	}

	fields, hasFields := settings["fields"].([]string)
	if !hasFields {
		fields = []string{}
		for _, c := range a.Columns {
			fields = append(fields, c.Name)
		}
	}
	fields = append(fields, idField)

	a.fields = fields
	a.idField = idField
}

func (a *Adapter) Update(params moleculer.Payload) moleculer.Payload {
	id := params.Get("id")
	if !id.Exists() {
		return payload.Error("Cannot update record without id")
	}
	return a.UpdateById(id, params.Remove("id"))
}

func (a *Adapter) UpdateById(id, update moleculer.Payload) moleculer.Payload {
	conn := a.getConn()
	if conn == nil {
		return noConnectionError()
	}
	changes, values := a.updatePairs(update)
	updtStmt := "UPDATE " + a.Table + " SET " + strings.Join(changes, ", ") + " WHERE id=" + id.String() + ";"
	a.log.Debug(updtStmt)
	if err := sqlitex.Exec(conn, updtStmt, nil, values...); err != nil {
		a.log.Error("Error on update: ", err)
		return payload.New(err)
	}
	a.returnConn(conn)
	a.log.Debug("update done.")
	return a.FindById(id)
}

func (a *Adapter) Insert(param moleculer.Payload) moleculer.Payload {
	conn := a.getConn()
	if conn == nil {
		return noConnectionError()
	}
	defer a.returnConn(conn)

	columns, values := a.insertFields(param)
	insert := "INSERT INTO " + a.Table + " (" + strings.Join(columns, ", ") + ") VALUES(" + strings.Join(placeholders(columns), ", ") + ") ;"
	if err := sqlitex.Exec(conn, insert, nil, values...); err != nil {
		a.log.Error("Error on insert: ", err)
		return payload.New(err)
	}
	return param.Add(a.idField, conn.LastInsertRowID())
}

func (a *Adapter) RemoveAll() moleculer.Payload {
	conn := a.getConn()
	if conn == nil {
		return noConnectionError()
	}
	defer a.returnConn(conn)

	delete := "DELETE FROM " + a.Table + " ;"
	a.log.Debug(delete)
	if err := sqlitex.Exec(conn, delete, nil); err != nil {
		a.log.Error("Error on delete: ", err)
		return payload.New(err)
	}
	deletedCount := conn.Changes()
	return payload.New(map[string]int{"deletedCount": deletedCount})
}

func (a *Adapter) RemoveById(id moleculer.Payload) moleculer.Payload {
	conn := a.getConn()
	if conn == nil {
		return noConnectionError()
	}
	defer a.returnConn(conn)

	delete := "DELETE FROM " + a.Table + " WHERE id = " + id.String() + " ;"
	a.log.Debug(delete)
	if err := sqlitex.Exec(conn, delete, nil); err != nil {
		a.log.Error("Error on delete: ", err)
		return payload.New(err)
	}
	deletedCount := conn.Changes()
	return payload.New(map[string]int{"deletedCount": deletedCount})
}

func resolveFindOptions(param moleculer.Payload) (limit, offset string, sort []string) {
	if param.Get("limit").Exists() {
		limit = param.Get("limit").String()
	}
	if param.Get("offset").Exists() {
		offset = param.Get("offset").String()
	}
	if param.Get("sort").Exists() {
		if param.Get("sort").IsArray() {
			sort = sortsFromStringArray(param.Get("sort"))
		} else {
			sort = sortsFromString(param.Get("sort"))
		}
	}
	return limit, offset, sort
}

func sortsFromString(sort moleculer.Payload) []string {
	parts := strings.Split(strings.Trim(sort.String(), " "), " ")
	if len(parts) > 1 {
		sorts := []string{}
		for _, value := range parts {
			sorts = append(sorts, sortEntry(value))
		}
		return sorts
	} else if len(parts) == 1 && parts[0] != "" {
		return []string{sortEntry(parts[0])}
	}
	fmt.Println("**** invalid Sort Entry **** ")
	return []string{}
}

func sortsFromStringArray(sort moleculer.Payload) []string {
	sorts := []string{}
	sort.ForEach(func(index interface{}, value moleculer.Payload) bool {
		sorts = append(sorts, sortEntry(value.String()))
		return true
	})
	return sorts
}

func sortEntry(entry string) string {
	if strings.Index(entry, "-") == 0 {
		entry = strings.Replace(entry, "-", "", 1) + " DESC"
	} else {
		entry = strings.Replace(entry, "-", "", 1) + " ASC"
	}
	return entry
}

func resolveFields(fields []string, param moleculer.Payload) []string {
	if param.Get("fields").Exists() && param.Get("fields").IsArray() {
		fields = param.Get("fields").StringArray()
	}
	return fields
}

func (adapter *Adapter) FindById(id moleculer.Payload) moleculer.Payload {
	filter := payload.New(map[string]interface{}{
		"query": map[string]interface{}{adapter.idField: id.Value()},
	})
	return adapter.FindOne(filter)
}

func (adapter *Adapter) FindByIds(params moleculer.Payload) moleculer.Payload {
	if !params.IsArray() {
		return payload.Error("FindByIds() only support lists!  --> !params.IsArray()")
	}
	r := payload.EmptyList()
	params.ForEach(func(idx interface{}, id moleculer.Payload) bool {
		r = r.AddItem(adapter.FindById(id))
		return true
	})
	return r
}

func (a *Adapter) FindOne(params moleculer.Payload) moleculer.Payload {
	params = params.Add("limit", 1)
	return a.Find(params).First()
}

func (a *Adapter) Count(param moleculer.Payload) moleculer.Payload {
	return a.query([]string{"COUNT(*) as count"}, param, func(fields []string, stmt *sqlite.Stmt) moleculer.Payload {
		count := stmt.GetInt64("count")
		return payload.New(map[string]int64{"count": count})
	}).First()
}
func (a *Adapter) Find(param moleculer.Payload) moleculer.Payload {
	fields := resolveFields(a.fields, param)
	return a.query(fields, param, a.rowToPayload)
}

type rowFactory func([]string, *sqlite.Stmt) moleculer.Payload

func (a *Adapter) query(fields []string, param moleculer.Payload, mapRow rowFactory) moleculer.Payload {
	conn := a.getConn()
	if conn == nil {
		return noConnectionError()
	}
	defer a.returnConn(conn)

	limit, offset, sort := resolveFindOptions(param)

	rows := []moleculer.Payload{}
	where := a.findWhere(param)
	selec := "SELECT " + strings.Join(fields, ", ") + " FROM " + a.Table
	if where != "" {
		selec = selec + " WHERE " + where
	}
	if len(sort) > 0 {
		selec = selec + " ORDER BY " + strings.Join(sort, ", ")
	}
	if limit != "" {
		selec = selec + " LIMIT " + limit
	}
	if offset != "" {
		selec = selec + " OFFSET " + offset
	}
	selec = selec + " ;"

	a.log.Debug(selec)
	if err := sqlitex.Exec(conn, selec, func(stmt *sqlite.Stmt) error {
		rows = append(rows, mapRow(fields, stmt))
		return nil
	}); err != nil {
		a.log.Error("Error on select: ", err)
		return payload.New(err)
	}
	return payload.New(rows)
}

func (a *Adapter) columnValue(column string, stmt *sqlite.Stmt) interface{} {
	t := a.columnType(column)
	if t == "NUMBER" {
		return stmt.GetFloat(column)
	}
	if t == "INTEGER" {
		return stmt.GetInt64(column)
	}
	return stmt.GetText(column)
}

func (a *Adapter) rowToPayload(fields []string, stmt *sqlite.Stmt) moleculer.Payload {
	data := map[string]interface{}{}
	for _, c := range fields {
		data[c] = a.columnValue(c, stmt)
	}
	return payload.New(data)
}

func (a *Adapter) columnType(field string) (r string) {
	for _, c := range a.Columns {
		if c.Name == field {
			return c.Type
		}
	}
	return r
}

func (a *Adapter) wrapValue(cType string, value moleculer.Payload) (r string) {
	if cType == "TEXT" || cType == "" {
		return "'" + value.String() + "'"
	}
	if cType == "NUMBER" {
		return "'" + strconv.FormatFloat(value.Float(), 'f', 6, 64) + "'"
	}
	if cType == "INTEGER" {
		return "'" + strconv.FormatInt(value.Int64(), 64) + "'"
	}

	return r
}

func (a *Adapter) filterPairs(query moleculer.Payload) (pairs []string) {
	query.ForEach(func(key interface{}, item moleculer.Payload) bool {
		field := key.(string)
		value := a.wrapValue(a.columnType(field), item)
		pairs = append(pairs, field+" = "+value)
		return true
	})
	return pairs
}

func (a *Adapter) updateWhere(params moleculer.Payload) string {
	where := ""
	queryPairs := a.filterPairs(params)
	if len(queryPairs) > 0 {
		where = strings.Join(queryPairs, " AND ")
	}
	return where
}

func (a *Adapter) findWhere(params moleculer.Payload) string {
	query := payload.Empty()
	if params.Get("query").Exists() {
		query = params.Get("query")
	}
	where := ""
	queryPairs := a.filterPairs(query)
	if len(queryPairs) > 0 {
		where = strings.Join(queryPairs, " AND ")
	}
	searchPairs := a.parseSearchFields(params)
	if len(searchPairs) > 0 {
		if where != "" {
			where = where + " AND "
		}
		where = where + "(" + strings.Join(searchPairs, " OR ") + ")"
	}
	return where
}

func (a *Adapter) parseSearchFields(params moleculer.Payload) (pairs []string) {
	searchFields := params.Get("searchFields")
	search := params.Get("search")
	searchValue := ""
	if search.Exists() {
		searchValue = search.String()
	}
	if searchFields.Exists() {
		if searchFields.IsArray() {
			fields := searchFields.StringArray()
			for _, field := range fields {
				pairs = append(pairs, field+" = "+"'"+searchValue+"'")
			}
		} else {
			pairs = []string{searchFields.String() + " = " + "'" + searchValue + "'"}
		}
	}
	return pairs
}
