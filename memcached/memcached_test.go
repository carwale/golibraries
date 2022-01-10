package memcached

import (
	"encoding/gob"
	"testing"
)

func TestBytesInterfaceConversionString(t *testing.T) {
	testString := "testString"
	got, err := GetBytes(testString)
	t.Logf("Bytes are %v", got)
	if err != nil {
		t.Errorf("Error in converting testString to bytes %s", err.Error())
	}
	res, err := BytesToEmptyInterface(got)
	t.Logf("string value after decoding: %s", res.(string))
	if err != nil {
		t.Errorf("cannot convert bytes to empty interface %s", err.Error())
	}
	if res.(string) != "testString" {
		t.Errorf("converting bytes to string went wrong %s asda", res.(string))
	}
}

func TestBytesInterfaceConversionInt(t *testing.T) {
	testInt := 600
	got, err := GetBytes(testInt)
	t.Logf("Bytes are %v", got)
	if err != nil {
		t.Errorf("Error in converting testint to bytes %s", err.Error())
	}
	res, err := BytesToEmptyInterface(got)
	t.Logf("int value after decoding: %d", res.(int))
	if err != nil {
		t.Errorf("cannot convert bytes to empty interface %s", err.Error())
	}
	if res.(int) != 600 {
		t.Errorf("converting bytes to int went wrong %d", res.(int))
	}
}

func TestBytesInterfaceConversionStruct(t *testing.T) {
	type Person struct {
		Name string
		Age  int
	}
	testStruct := Person{Name: "Somil", Age: 23}
	gob.Register(testStruct)
	got, err := GetBytes(testStruct)
	t.Logf("Bytes are %v", got)
	if err != nil {
		t.Errorf("Error in converting teststruct to bytes %s", err.Error())
	}
	res, err := BytesToEmptyInterface(got)
	resStruct := res.(Person)
	t.Logf("struct value after decoding: %v", resStruct)
	if err != nil {
		t.Errorf("cannot convert bytes to empty interface %s", err.Error())
	}
	if resStruct.Age != 23 && resStruct.Name != "Somil" {
		t.Errorf("converting bytes to int went wrong %v", resStruct)
	}
}

func TestGetBytesInvalid(t *testing.T) {
	got, err := GetBytes(nil)
	t.Logf("Bytes are %v", got)
	if err == nil {
		t.Errorf("Error in converting teststruct to bytes %s", err.Error())
	}
}

func TestBytesToEmptyInterfaceInvalid(t *testing.T) {
	_, err := BytesToEmptyInterface(nil)
	if err == nil {
		t.Error("Error not thrown despite of passing nil")
	}
	t.Logf("Error is: %s", err.Error())
}

func TestCreateMemcacheObjectCreation(t *testing.T) {
	key := "testKey"
	value := "testVal"
	obj, err := CreateMemCacheObject(key, value, 10)
	if err != nil {
		t.Errorf("CreateMemCacheObject FAILED. Expected *memcache.Item object, got error. ERROR: %s", err.Error())
	}
	resVal, err := BytesToEmptyInterface(obj.Value)
	if err != nil {
		t.Errorf("bytesToEmptyInterface FAILED. Expected interface{} object, got error. ERROR: %s", err.Error())
	}
	if obj.Key == "testKey" && resVal.(string) == "testVal" {
		t.Logf("TestCreateMemcacheObjectCreation PASSED")
	} else {
		t.Errorf("Test FAILED. Expected key as %s and value as %s, got key as %s and value as %s", key, value, obj.Key, resVal.(string))
	}

}
