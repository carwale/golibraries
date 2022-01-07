package memcached

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/bradfitz/gomemcache/memcache"
)

// CacheClient is used to add,update,remove items from memcache
type CacheClient struct {
	client *memcache.Client
}

// GetBytes converts interface{} to a byte array
func GetBytes(key interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if key == nil {
		return nil, fmt.Errorf("input cannot be nil")
	}
	err := enc.Encode(&key)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BytesToEmptyInterface converts byte array to interface{} object
func BytesToEmptyInterface(data []byte) (interface{}, error) {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	var res interface{}
	err := dec.Decode(&res)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// CreateMemCacheObject creates a *memcache.Item
// It takes in key, value and expiration time.
// expiration is the cache expiration time, in seconds: either a relative
// time from now (up to 1 month), or an absolute Unix epoch time.
// Zero means the Item has no expiration time.
// It returns (nil, err) if there's any other error, else returns a *memcache.Item
func CreateMemCacheObject(key string, value interface{}, expiration int32) (*memcache.Item, error) {
	valueBytes, err := GetBytes(value)
	if err != nil {
		return nil, err
	}
	return &memcache.Item{Key: key, Value: valueBytes, Expiration: expiration}, nil
}

// NewMemCachedClient returns a connected client server to cache to.
// It returns the *CacheClient object if successful, else returns (nil,err)
func NewMemCachedClient(serverList []string) (*CacheClient, error) {
	memCacheClient := memcache.New(serverList...)
	err := memCacheClient.Ping()
	if err != nil {
		return nil, err
	}
	c := &CacheClient{
		client: memCacheClient,
	}
	return c, nil
}

// GetItem takes in the key, expiration and a dbCallBack function.
// If a cache miss occurs, the dbCallBack function is called which retrieves data from the database.
// This value from the database is saved back to memcache.
// expiration is the cache expiration time, in seconds: either a relative
// time from now (up to 1 month), or an absolute Unix epoch time.
// Zero means the Item has no expiration time.
// It returns (nil, err) if there's any other error, else returns an interface{} object.
func (c *CacheClient) GetItem(key string, expiration int32, dbCallBack func() (interface{}, error)) (interface{}, error) {
	item, err := c.client.Get(key)
	if err != nil {
		if err == memcache.ErrCacheMiss {
			value, err := dbCallBack()
			if err != nil {
				return nil, err
			}
			_, err = c.AddItem(key, value, expiration)
			if err != nil {
				return nil, err
			}
			return value, nil
		}
		return nil, err
	}
	res, err := BytesToEmptyInterface(item.Value)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// AddItem saves an Item to cache.
// It returns false,error if it is unable to save the Item.
// expiration is the cache expiration time, in seconds: either a relative
// time from now (up to 1 month), or an absolute Unix epoch time.
// Zero means the Item has no expiration time.
func (c *CacheClient) AddItem(key string, value interface{}, expiration int32) (bool, error) {
	item, err := CreateMemCacheObject(key, value, expiration)
	if err != nil {
		return false, err
	}
	err = c.client.Add(item)
	if err != nil {
		return false, err
	}
	return true, nil
}

// UpdateItem updates an Item in cache. If addIfNotExists is true, the item is added if the key doesn't exist.
// It returns error if it is unable to update the Item.
// expiration is the cache expiration time, in seconds: either a relative
// time from now (up to 1 month), or an absolute Unix epoch time.
// Zero means the Item has no expiration time.
func (c *CacheClient) UpdateItem(key string, value interface{}, expiration int32, addIfNotExists bool) (bool, error) {
	item, err := CreateMemCacheObject(key, value, expiration)
	if err != nil {
		return false, err
	}
	err = c.client.Replace(item)
	if err != nil {
		//unable to find key in cache
		if addIfNotExists {
			return c.AddItem(key, value, expiration)
		}
		return false, err
	}
	return true, nil
}

// DeleteWithoutDelay deletes a given key from the server without any delay
// It returns false,error if delete was unsuccessful.
func (c *CacheClient) DeleteWithoutDelay(key string) (bool, error) {
	err := c.client.Delete(key)
	if err != nil {
		return false, err
	}
	return true, nil
}

// DeleteItem deletes a given key from the server after the delay mentioned.
// It returns false, error if the operation was unsuccessful.
// key is the memcache key to be deleted.
// delay is the time after which the key should be deleted, in seconds: either a relative
// time from now (up to 1 month), or an absolute Unix epoch time.
func (c *CacheClient) DeleteItem(key string, delay int32) (bool, error) {
	item, err := c.client.Get(key)
	if err != nil {
		return false, err
	}
	newItem := &memcache.Item{Key: item.Key, Value: item.Value, Expiration: delay}
	err = c.client.Replace(newItem)
	if err != nil {
		return false, err
	}
	return true, nil
}
