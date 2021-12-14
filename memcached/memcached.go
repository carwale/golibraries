package memcached

import (
	"github.com/bradfitz/gomemcache/memcache"
)

// CacheClient is used to add,update,remove items from memcache
type CacheClient struct {
	client *memcache.Client
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

// getItem retrieves an Item from cache
// It returns (nil, err) if it is not able to retrieve the item, else returns (Item,nil)
func (c *CacheClient) getItem(key string) (*memcache.Item, error) {
	item, err := c.client.Get(key)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// saveItem saves an Item to cache.
// It returns error if it is unable to save the Item.
func (c *CacheClient) saveItem(item *memcache.Item) error {
	err := c.client.Add(item)
	if err != nil {
		return err
	}
	return nil
}

// updateItem updates an Item in cache. If adds the item if the key doesn't exist
// It returns error if it is unable to update the Item.
func (c *CacheClient) updateItem(item *memcache.Item) error {
	err := c.client.Replace(item)
	if err != nil {
		//unable to find key in cache
		err = c.saveItem(item)
		if err != nil {
			return err
		}
		return nil
	}
	return nil
}

// deleteItem deletes a given key from the server.
// It returns error if delete was unsuccessful.
func (c *CacheClient) deleteItem(key string) error {
	err := c.client.Delete(key)
	if err != nil {
		return err
	}
	return nil
}
