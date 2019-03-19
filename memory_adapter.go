package db

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/go-memdb"
	"github.com/moleculer-go/moleculer"
	"github.com/moleculer-go/moleculer/payload"
	"github.com/moleculer-go/moleculer/util"
)

//MemoryAdapter stores data in memory!
type MemoryAdapter struct {
	Schema *memdb.DBSchema
	Table  string
	db     *memdb.MemDB
}

func (adapter *MemoryAdapter) Connect() error {
	db, err := memdb.NewMemDB(adapter.Schema)
	if err != nil {
		return err
	}
	adapter.db = db
	return nil
}

func (adapter *MemoryAdapter) Disconnect() error {
	adapter.db = nil
	return nil
}

func (adapter *MemoryAdapter) Find(params moleculer.Payload) moleculer.Payload {
	indexName := strings.Join(params.Get("searchFields").StringArray(), "-")
	search := params.Get("search").String()
	tx := adapter.db.Txn(false)
	defer tx.Abort()
	results, err := tx.Get(adapter.Table, indexName, search)
	if err != nil {
		return payload.Error("Failed trying to find. Error: ", err.Error())
	}
	items := []moleculer.Payload{}
	for {
		value := results.Next()
		if value == nil {
			break
		}
		items = append(items, payload.Create(value))
	}
	return payload.Create(items)
}

func (adapter *MemoryAdapter) FindOne(params moleculer.Payload) moleculer.Payload {
	indexName := strings.Join(params.Get("searchFields").StringArray(), "-")
	search := params.Get("search").String()
	tx := adapter.db.Txn(false)
	defer tx.Abort()
	result, err := tx.First(adapter.Table, indexName, search)
	if err != nil {
		return payload.Error("Failed trying to find. Error: ", err.Error())
	}
	return payload.Create(result)
}

func (adapter *MemoryAdapter) FindById(params moleculer.Payload) moleculer.Payload {
	params = params.Add(map[string]interface{}{
		"searchFields": []string{"id"},
		"search":       params.Get("id").String(),
	})
	return adapter.FindOne(params)
}

func (adapter *MemoryAdapter) FindByIds(params moleculer.Payload) moleculer.Payload {
	ids := params.Get("ids").StringArray()
	list := []moleculer.Payload{}
	for id := range ids {
		list = append(list, adapter.FindById(payload.Create(map[string]interface{}{
			"id": id,
		})))
	}
	return payload.Create(list)
}

func (adapter *MemoryAdapter) Count(params moleculer.Payload) moleculer.Payload {
	result := adapter.Find(params)
	return payload.Create(result.Len())
}

func (adapter *MemoryAdapter) Insert(params moleculer.Payload) moleculer.Payload {
	params = params.Add(map[string]interface{}{
		"id": util.RandomString(12),
	})
	tx := adapter.db.Txn(true)
	err := tx.Insert(adapter.Table, params)
	if err != nil {
		defer tx.Abort()
		return payload.Error("Failed trying to find. Error: ", err.Error())
	}
	defer tx.Commit()
	return params
}

func (adapter *MemoryAdapter) Update(params moleculer.Payload) moleculer.Payload {
	one := adapter.FindById(params)
	if !one.IsError() && one.Exists() {
		tx := adapter.db.Txn(true)
		err := tx.Delete(adapter.Table, one.Value())
		if err != nil {
			defer tx.Abort()
			return payload.Error("Failed trying to update record. source error: ", err.Error())
		}
		rec := one.Add(params.RawMap())
		err = tx.Insert(adapter.Table, rec)
		if err != nil {
			defer tx.Abort()
			return payload.Error("Failed trying to update record. source error: ", err.Error())
		}
		defer tx.Commit()
		return rec
	}
	return payload.Error("Failed trying to update record. Could not find record with id: ", params.Get("id").String())
}

func (adapter *MemoryAdapter) UpdateById(params moleculer.Payload) moleculer.Payload {
	return adapter.Update(params)
}

func (adapter *MemoryAdapter) RemoveById(params moleculer.Payload) moleculer.Payload {
	one := adapter.FindById(params)
	if !one.IsError() && one.Exists() {
		tx := adapter.db.Txn(true)
		err := tx.Delete(adapter.Table, one.Value())
		if err != nil {
			defer tx.Abort()
			return payload.Error("Failed trying to removed record. source error: ", err.Error())
		}
		defer tx.Commit()
		return params
	}
	return nil
}

type PayloadIndex struct {
	Field     string
	Lowercase bool
}

func (s *PayloadIndex) FromArgs(args ...interface{}) ([]byte, error) {
	key := ""
	for _, item := range args {
		s, ok := item.(string)
		if !ok {
			return nil, errors.New("Indexer can only handler string arguments.")
		}
		if key != "" {
			key = key + "-"
		}
		key = key + s
	}
	if s.Lowercase {
		key = strings.ToLower(key)
	}
	key += "\x00"
	return []byte(key), nil
}

func (s *PayloadIndex) FromObject(obj interface{}) (bool, []byte, error) {
	p, isPayload := obj.(moleculer.Payload)
	m, isMap := obj.(map[string]interface{})
	if !isPayload && !isMap {
		return false, nil, errors.New("Invalid type. It must be moleculer.Payload!")
	}
	if isMap {
		p = payload.Create(m)
	}
	if !p.Get(s.Field).Exists() {
		fmt.Println("obj --> ", obj)
		return false, nil, errors.New(fmt.Sprint("Field `", s.Field, "` not found!"))
	}
	svalue := p.Get(s.Field).String()
	if s.Lowercase {
		svalue = strings.ToLower(svalue)
	}
	svalue += "\x00"
	return true, []byte(svalue), nil
}
