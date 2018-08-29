package gofiledb

import (
	"encoding/gob"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

/********************************************************************************
* C L I E N T
*********************************************************************************/

// Client is the primary object that the application interacts with while saving or fetching data
type Client struct {
	ClientParams
	collections   *collectionStore
	isInitialized bool // IsInitialized ensures that we don't initialize the client more than once, since doing that could lead to issues
	sync.RWMutex
}

type ClientParams struct {
	documentRoot       string // documentRoot is the absolute path to the directory that can be used for storing the files/data
	numPartitions      int    // numPartitions determines how many sub-folders should the package create inorder to partition the data
	ignorePreviousData bool
	enableGzip         bool
}

type collectionStore struct {
	Store map[string]Collection
	sync.RWMutex
}

func NewClientParams(documentRoot string, numPartitions int) ClientParams {
	var params ClientParams = ClientParams{
		documentRoot:  documentRoot,
		numPartitions: numPartitions,
	}
	return params
}

/*** Local Getters ***/

func (c *Client) getDocumentRoot() string {
	return c.documentRoot
}
func (c *Client) getIsInitialized() bool {
	return c.isInitialized
}
func (c *Client) getCollections() *collectionStore {
	return c.collections
}
func (c *Client) setCollections(cl *collectionStore) {
	c.collections = cl
}
func (c *Client) getCollectionByName(collectionName string) (*Collection, error) {
	c.collections.RLock()
	defer c.collections.RUnlock()

	cl, hasKey := c.collections.Store[collectionName]
	if !hasKey {
		return nil, ErrCollectionDoesNotExist
	}
	return &cl, nil
}
func (c *Client) Destroy() error {
	// remove everything related to this client, and refresh it
	err := os.RemoveAll(c.getDocumentRoot())
	if err != nil {
		return err
	}
	c = &Client{}
	return nil
}
func (c *Client) FlushAll() error {
	return os.RemoveAll(c.documentRoot)
}

/*** Add Collection ***/

func (c *Client) AddCollection(p CollectionProps) error {

	// Sanitize the collection props
	p = p.sanitize()

	// Validate the collection props
	err := p.validate()
	if err != nil {
		return err
	}

	// Create a Colelction and add to registered collections
	var cl Collection
	cl.CollectionProps = p

	// Don't repeat collection names
	c.registeredCollections.RLock()
	_, hasKey := c.registeredCollections.Store[p.Name]
	c.registeredCollections.RUnlock()
	if hasKey {
		return fmt.Errorf("A collection with name %s already exists", p.Name)
	}

	// Create the required dir paths for this collection
	cl.DirPath = c.getDirPathForCollection(p.Name)
	// create the dirs for the collection
	err = createDirIfNotExist(joinPath(cl.DirPath, META_DIR_NAME))
	if err != nil {
		return err
	}
	// for indexes
	err = createDirIfNotExist(joinPath(cl.DirPath, META_DIR_NAME, "index"))
	if err != nil {
		return err
	}
	err = createDirIfNotExist(joinPath(cl.DirPath, DATA_DIR_NAME))
	if err != nil {
		return err
	}
	// Initialize the IndexStore, which stores info on the indexes associated with this Collection
	cl.IndexStore.Store = make(map[string]IndexInfo)

	// Register the Collection

	c.registeredCollections.Lock()
	defer c.registeredCollections.Unlock()

	// Initialize the collection store if not initialized (but it should already be initialized because of the Initialize() function)
	if c.registeredCollections.Store == nil {
		c.registeredCollections.Store = make(map[string]Collection)
	}
	c.registeredCollections.Store[p.Name] = cl

	err = c.setGlobalMetaStruct("registered_collections.gob", c.registeredCollections.Store)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) RemoveCollection(collectionName string) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}

	// Unregister the collection from the Client's Collection Store
	c.registeredCollections.Lock()
	defer c.registeredCollections.Unlock()
	clog.Infof("Removing collection registration...")
	delete(c.registeredCollections.Store, collectionName)

	err = c.setGlobalMetaStruct("registered_collections.gob", c.registeredCollections.Store)
	if err != nil {
		return err
	}

	// Delete all the data & meta dirs for that collection
	clog.Infof("Deleting data at %s...", cl.DirPath)
	err = os.RemoveAll(cl.DirPath)
	if err != nil {
		return err
	}

	return nil
}

/*** Data Writers ***/

func (c *Client) Set(collectionName string, key string, data []byte) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}

	return cl.set(key, data)
}

func (c *Client) SetStruct(collectionName string, key string, v interface{}) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}

	return cl.setFromStruct(key, v)
}

func (c *Client) SetFromReader(collectionName, key string, src io.Reader) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}

	return cl.setFromReader(key, src)
}

func (c *Client) setGlobalMetaStruct(metaName string, v interface{}) error {
	file, err := os.Create(joinPath(c.getDocumentRoot(), META_DIR_NAME, metaName))
	if err != nil {
		return err
	}

	enc := gob.NewEncoder(file)
	err = enc.Encode(v)
	if err != nil {
		return err
	}
	return nil
}

/*** Data Readers ***/

func (c *Client) GetFile(collectionName, key string) (*os.File, error) {
	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return nil, err
	}

	return cl.getFile(key)
}

func (c *Client) Get(collectionName string, key string) ([]byte, error) {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return nil, err
	}

	return cl.getFileData(key)
}

func (c *Client) GetIfExist(collectionName string, key string) ([]byte, error) {

	data, err := c.Get(collectionName, key)
	if os.IsNotExist(err) { // if doesn't exist, return nil
		return nil, nil
	}
	return data, err
}

func (c *Client) GetStruct(collectionName string, key string, dest interface{}) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}

	return cl.getIntoStruct(key, dest)
}

func (c *Client) GetStructIfExists(collectionName string, key string, dest interface{}) error {

	err := c.GetStruct(collectionName, key, dest)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (c *Client) GetIntoWriter(collectionName, key string, dest io.Writer) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}
	return cl.getIntoWriter(key, dest)
}

func (c *Client) getGlobalMetaStruct(metaName string, v interface{}) error {
	file, err := os.Open(joinPath(c.getDocumentRoot(), META_DIR_NAME, metaName))
	if err != nil {
		return err
	}
	dec := gob.NewDecoder(file)
	err = dec.Decode(v)
	if err != nil {
		return err
	}
	return nil
}

/** Searchers **/
// Todo: search()
func (c *Client) Search(collectionName string, query string) ([]interface{}, error) {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return nil, err
	}

	return cl.search(query)
}

func (c *Client) AddIndex(collectionName string, fieldLocator string) error {

	cl, err := c.getCollectionByName(collectionName)
	if err != nil {
		return err
	}

	return cl.addIndex(fieldLocator)
}

/*** Navigation Helpers ***/

// func (c *Client) getFilePath(collectionName, key string) string {
// 	return c.getDirPathForData(collectionName, key) + string(os.PathSeparator) + key
// }

// func (c *Client) getDirPathForData(collectionName, key string) string {
// 	collectionDirPath := c.getDirPathForCollection(collectionName)
// 	dirs := []string{collectionDirPath, DATA_DIR_NAME, c.getPartitionDirName(key)}
// 	return strings.Join(dirs, string(os.PathSeparator))
// }

func (c *Client) getDirPathForCollection(collectionName string) string {
	dirs := []string{c.documentRoot, DATA_DIR_NAME, collectionName}
	return strings.Join(dirs, string(os.PathSeparator))
}

func (c *Client) getPartitionDirName(key string) string {
	h := getPartitionHash(key, c.numPartitions)
	return DATA_PARTITION_PREFIX + h
}

/********************************************************************************
* C L I E N T  P A R A M S
*********************************************************************************/

func (p ClientParams) validate() error {
	// documentRoot shall not be totally white
	if strings.TrimSpace(p.documentRoot) == "" {
		return fmt.Errorf("Empty documentRoot field provided")
	}
	// numPartitions shall be positive
	if p.numPartitions < 1 {
		return fmt.Errorf("Invalid numPartitions value provided: %d", p.numPartitions)
	}
	// documentRoot shall exist as a directory
	info, err := os.Stat(p.documentRoot)
	if os.IsNotExist(err) {
		return fmt.Errorf("no directory found at path %s", p.documentRoot)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s path is not a directory", p.documentRoot)
	}

	return nil
}

func (p ClientParams) sanitize() ClientParams {

	// remove trailing path separator characters (e.g. / in Linux) from the documentRoot
	if len(p.documentRoot) > 0 && p.documentRoot[len(p.documentRoot)-1] == os.PathSeparator {
		p.documentRoot = p.documentRoot[:len(p.documentRoot)-1]
		return p.sanitize()
	}

	// create a new folder at the path provided
	p.documentRoot = p.documentRoot + string(os.PathSeparator) + "gofiledb_warehouse"

	return p

}