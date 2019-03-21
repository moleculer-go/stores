package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moleculer-go/moleculer/payload"

	"github.com/moleculer-go/moleculer"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

//MongoAdapter Mongo DB Adapter :)
type MongoAdapter struct {
	MongoURL   string
	Timeout    time.Duration
	Database   string
	Collection string
	client     *mongo.Client
	coll       *mongo.Collection
}

// Connect connect to mongo, stores the client and the collection.
func (adapter *MongoAdapter) Connect() error {
	ctx, _ := context.WithTimeout(context.Background(), adapter.Timeout)
	var err error
	adapter.client, err = mongo.Connect(ctx, options.Client().ApplyURI(adapter.MongoURL))
	if err != nil {
		return err
	}
	err = adapter.client.Ping(ctx, readpref.Primary())
	if err != nil {
		return err
	}
	adapter.coll = adapter.client.Database(adapter.Database).Collection(adapter.Collection)
	return nil
}

// Disconnect disconnects from mongo.
func (adapter *MongoAdapter) Disconnect() error {
	ctx, _ := context.WithTimeout(context.Background(), adapter.Timeout)
	return adapter.client.Disconnect(ctx)
}

func parseSearchFields(params, query moleculer.Payload) moleculer.Payload {
	searchFields := params.Get("searchFields")
	search := params.Get("search")
	searchValue := ""
	if search.Exists() {
		searchValue = search.String()
	}
	if searchFields.Exists() {
		fields := searchFields.StringArray()
		if len(fields) == 1 {
			query = query.Add(fields[0], searchValue)
		} else if len(fields) > 1 {
			or := payload.EmptyList()
			for _, field := range fields {
				bm := bson.M{}
				bm[field] = searchValue
				or = or.AddItem(bm)
			}
			query = query.Add("$or", or)
		}
	}
	return query
}

func parseFindOptions(params moleculer.Payload) *options.FindOptions {
	opts := options.FindOptions{}
	limit := params.Get("limit")
	offset := params.Get("offset")
	sort := params.Get("sort")
	if limit.Exists() {
		v := limit.Int64()
		opts.Limit = &v
	}
	if offset.Exists() {
		v := offset.Int64()
		opts.Skip = &v
	}
	if sort.Exists() {
		if sort.IsArray() {
			opts.Sort = sortsFromStringArray(sort)
		} else {
			opts.Sort = sortsFromString(sort)
		}

	}
	return &opts
}

func sortEntry(entry string) bson.E {
	item := bson.E{entry, 1}
	if strings.Index(entry, "-") == 0 {
		entry = strings.Replace(entry, "-", "", 1)
		item = bson.E{entry, -1}
	}
	return item
}

func sortsFromString(sort moleculer.Payload) bson.D {
	parts := strings.Split(strings.Trim(sort.String(), " "), " ")
	if len(parts) > 1 {
		sorts := bson.D{}
		for _, value := range parts {
			item := sortEntry(value)
			sorts = append(sorts, item)
		}
		return sorts
	} else if len(parts) == 1 && parts[0] != "" {
		return bson.D{sortEntry(parts[0])}
	}
	fmt.Println("**** invalid Sort Entry **** ")
	return nil

}

func sortsFromStringArray(sort moleculer.Payload) bson.D {
	sorts := bson.D{}
	sort.ForEach(func(index interface{}, value moleculer.Payload) bool {
		item := sortEntry(value.String())
		sorts = append(sorts, item)
		return true
	})
	return sorts
}

func parseFilter(params moleculer.Payload) bson.M {
	query := payload.Empty()
	if params.Get("query").Exists() {
		query = params.Get("query")
	}
	query = parseSearchFields(params, query)
	return query.Bson()
}

func (adapter *MongoAdapter) openCursor(params moleculer.Payload) (*mongo.Cursor, context.Context, error) {
	ctx, _ := context.WithTimeout(context.Background(), adapter.Timeout)
	filter := parseFilter(params)
	opts := parseFindOptions(params)
	cursor, err := adapter.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, err
	}
	return cursor, ctx, nil
}

func cursorToPayload(ctx context.Context, cursor *mongo.Cursor) moleculer.Payload {
	list := []moleculer.Payload{}
	for cursor.Next(ctx) {
		var result bson.M
		err := cursor.Decode(&result)
		if err != nil {
			return payload.Create(err)
		}
		list = append(list, payload.Create(result))
	}
	if err := cursor.Err(); err != nil {
		return payload.Create(err)
	}
	return payload.Create(list)
}

// Find search the data store with the params provided.
func (adapter *MongoAdapter) Find(params moleculer.Payload) moleculer.Payload {
	cursor, ctx, err := adapter.openCursor(params)
	if err != nil {
		return payload.Create(err)
	}
	defer cursor.Close(ctx)
	return cursorToPayload(ctx, cursor)
}

func (adapter *MongoAdapter) FindOne(params moleculer.Payload) moleculer.Payload {
	params = params.Add("limit", 1)
	list := adapter.Find(params).Array()
	if len(list) == 0 {
		return nil
	}
	return list[0]
}
func (adapter *MongoAdapter) FindById(params moleculer.Payload) moleculer.Payload {
	id := params.Value()
	//maybe I need to convert the id value here.
	filter := payload.Create(bson.M{
		"query": bson.M{
			"_id": id,
		},
		"limit": 1,
	})
	fmt.Println("filter --> ", filter)
	items := adapter.Find(filter)
	fmt.Println("items --> ", items)
	if items.IsError() {
		return items
	}
	return items.First()
}
func (adapter *MongoAdapter) FindByIds(params moleculer.Payload) moleculer.Payload {
	return nil
}

// Count count the number of records for the given filter.
func (adapter *MongoAdapter) Count(params moleculer.Payload) moleculer.Payload {
	ctx, _ := context.WithTimeout(context.Background(), adapter.Timeout)
	filter := parseFilter(params)
	count, err := adapter.coll.CountDocuments(ctx, filter)
	if err != nil {
		return payload.Create(err)
	}
	return payload.Create(count)
}

func (adapter *MongoAdapter) Insert(params moleculer.Payload) moleculer.Payload {
	ctx, _ := context.WithTimeout(context.Background(), adapter.Timeout)
	res, err := adapter.coll.InsertOne(ctx, params.Bson())
	if err != nil {
		return payload.Error("Error while trying to insert record. Error: ", err.Error())
	}
	return params.Add("id", res.InsertedID)
}

func (adapter *MongoAdapter) Update(params moleculer.Payload) moleculer.Payload {
	return nil
}

func (adapter *MongoAdapter) UpdateById(params moleculer.Payload) moleculer.Payload {
	return nil
}
func (adapter *MongoAdapter) RemoveById(params moleculer.Payload) moleculer.Payload {
	return nil
}

func (adapter *MongoAdapter) RemoveAll() moleculer.Payload {
	ctx, _ := context.WithTimeout(context.Background(), adapter.Timeout)
	res, err := adapter.coll.DeleteMany(ctx, bson.M{})
	if err != nil {
		return payload.Error("Error while trying to remove all records. Error: ", err.Error())
	}
	return payload.Create(res.DeletedCount)
}
