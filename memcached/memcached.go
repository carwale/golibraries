package memcached

import (
	"github.com/bradfitz/gomemcache/memcache"
)

// initCache returns a connected client server to cache to.
// It returns the *memcache.Client object if successful, else returns (nil,err)
func initCache(serverList []string) (*memcache.Client, error) {
	server := memcache.New(serverList...)
	err := server.Ping()
	if err != nil {
		return nil, err
	}
	return server, nil
}

// getItem retrieves an Item from cache
// It returns (nil, err) if it is not able to retrieve the item, else returns (Item,nil)
func getItem(server *memcache.Client, key string) (*memcache.Item, error) {
	item, err := server.Get(key)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// saveItem saves an Item to cache.
// It returns error if it is unable to save the Item.
func saveItem(server *memcache.Client, item *memcache.Item) error {
	err := server.Add(item)
	if err != nil {
		return err
	}
	return nil
}

// updateItem updates an Item in cache.
// It returns error if it is unable to update the Item.
func updateItem(server *memcache.Client, item *memcache.Item) error {
	err := server.Replace(item)
	if err != nil {
		//unable to find key in cache
		err = saveItem(server, item)
		if err != nil {
			return err
		}
		return nil
	}
	return nil
}

// deleteItem deletes a given key from the server.
// It returns error if delete was unsuccessful.
func deleteItem(server *memcache.Client, key string) error {
	err := server.Delete(key)
	if err != nil {
		return err
	}
	return nil
}
