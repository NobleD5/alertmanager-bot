package translation

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"golang.org/x/text/message/catalog"
	"gopkg.in/yaml.v2"
)

type dictionary struct {
	Data map[string]string
}

func (d *dictionary) Lookup(key string) (data string, ok bool) {

	if value, ok := d.Data[key]; ok {
		return "\x02" + value, true
	}

	return "\x02" + d.Data[key], true
}

// ParseYAMLDict is parsing available dicts
func ParseYAMLDict(dirname string, logger log.Logger) (map[string]catalog.Dictionary, error) {

	translations := map[string]catalog.Dictionary{}

	fi, error := os.Stat(dirname)
	if error != nil {
		return nil, fmt.Errorf("error while os.Stat: %s", error.Error())
	}

	switch mode := fi.Mode(); {

	case mode.IsDir():

		files, err := ioutil.ReadDir(dirname)
		if err != nil {
			return nil, fmt.Errorf("err reading given directory: %s", err.Error())
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("directory is empty")
		}

		level.Debug(logger).Log("files", fmt.Sprint(files))

		for _, file := range files {

			level.Debug(logger).Log("file", fmt.Sprint(file))

			path := dirname + "/" + file.Name()

			yamlFile, err := ioutil.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("err reading given filename (%s): %s", yamlFile, err.Error())
			}

			data := map[string]string{}
			err = yaml.Unmarshal(yamlFile, &data)
			if err != nil {
				return nil, fmt.Errorf("err unmarshaling given yaml input: %s", err.Error())
			}

			lang := strings.Split(file.Name(), ".")[0]
			translations[lang] = &dictionary{Data: data}
		}

	case mode.IsRegular():

		yamlFile, err := ioutil.ReadFile(dirname)
		if err != nil {
			return nil, fmt.Errorf("err reading given filename (%s): %s", yamlFile, err.Error())
		}

		data := map[string]string{}
		err = yaml.Unmarshal(yamlFile, &data)
		if err != nil {
			return nil, fmt.Errorf("err unmarshaling given yaml input: %s", err.Error())
		}

		_, file := filepath.Split(dirname)
		lang := strings.Split(file, ".")[0]
		translations[lang] = &dictionary{Data: data}
	}

	return translations, nil
}
