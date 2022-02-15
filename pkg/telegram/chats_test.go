package telegram

import (
	// "encoding/json"
	// "net/http"
	// "net/http/httptest"
	// "net/url"
	// "os"
	"testing"
	// "time"

	"github.com/docker/libkv/store"
	"github.com/docker/libkv/store/boltdb"
	telebot "gopkg.in/tucnak/telebot.v2"
)

////////////////////////////////////////////////////////////////////////////////
// TESTING
////////////////////////////////////////////////////////////////////////////////

func TestChats(t *testing.T) {

	kvStore, err := boltdb.New([]string{"../test/kv.boltdb"}, &store.Config{Bucket: "alertmanager"})
	if err != nil {
		t.Errorf("boltdb.New() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("boltdb.New() : Test 1 PASSED.")
	}
	defer kvStore.Close()

	s, err := NewChatStore(kvStore)
	if err != nil {
		t.Errorf("NewChatStore() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("NewChatStore() : Test 1 PASSED.")
	}

	err = s.Add(telebot.Chat{ID: int64(1111)})
	if err != nil {
		t.Errorf("Add() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("Add() : Test 1 PASSED.")
	}

	l, err := s.List()
	if err != nil && l[0].ID == int64(1111) {
		t.Errorf("List() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("List() : Test 1 PASSED")
	}

	err = s.Remove(telebot.Chat{ID: int64(1111)})
	if err != nil {
		t.Errorf("Remove() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("Remove() : Test 1 PASSED.")
	}

	_, err = s.List()
	if err != nil && err.Error() == "Key not found in store" {
		t.Log("List() : Test 1 PASSED")
	} else {
		t.Errorf("List() : Test 1 FAILED, got error: %s", err)
	}
}
