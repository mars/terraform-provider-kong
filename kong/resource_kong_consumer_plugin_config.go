package kong

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/terraform/helper/schema"
	"github.com/kevholditch/gokong"
)

func resourceKongConsumerPluginConfig() *schema.Resource {
	return &schema.Resource{
		Create: resourceKongConsumerPluginConfigCreate,
		Read:   resourceKongConsumerPluginConfigRead,
		Delete: resourceKongConsumerPluginConfigDelete,

		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"consumer_id": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"plugin_name": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"config": &schema.Schema{
				Type:          schema.TypeMap,
				ForceNew:      true,
				Optional:      true,
				Elem:          schema.TypeString,
				Default:       nil,
				ConflictsWith: []string{"config_json"},
			},
			// Suppress diff when config is empty so we can sync with upstream always
			// The ForceNew property is what makes this work.
			"config_json": &schema.Schema{
				Type:          schema.TypeString,
				ForceNew:      true,
				Optional:      true,
				StateFunc:     normalizeDataJSON,
				ValidateFunc:  validateDataJSON,
				ConflictsWith: []string{"config"},
				Description:   "JSON format of plugin config",
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					return new == ""
				},
			},
		},
	}
}

type idFields struct {
	consumerId string
	pluginName string
	id         string
}

func validateDataJSON(configI interface{}, k string) ([]string, []error) {
	dataJSON := configI.(string)
	dataMap := map[string]interface{}{}
	err := json.Unmarshal([]byte(dataJSON), &dataMap)
	if err != nil {
		return nil, []error{err}
	}
	return nil, nil
}

func normalizeDataJSON(configI interface{}) string {
	dataJSON := configI.(string)

	dataMap := map[string]interface{}{}
	err := json.Unmarshal([]byte(dataJSON), &dataMap)
	if err != nil {
		// The validate function should've taken care of this.
		log.Printf("[ERROR] Invalid JSON data in config_json: %s", err)
		return ""
	}

	ret, err := json.Marshal(dataMap)
	if err != nil {
		// Should never happen.
		log.Printf("[ERROR] Problem normalizing JSON for config_json: %s", err)
		return dataJSON
	}

	return string(ret)
}

func buildId(consumerId, pluginName, configId string) string {
	return consumerId + "|" + pluginName + "|" + configId
}

func splitIdIntoFields(id string) (*idFields, error) {
	idSplit := strings.Split(id, "|")

	if len(idSplit) != 3 {
		return nil, fmt.Errorf("failed to calculate consumer plugin config id, should be pipe separated as consumerId|pluginName|id found: %v", id)
	}

	return &idFields{
		consumerId: idSplit[0],
		pluginName: idSplit[1],
		id:         idSplit[2],
	}, nil
}

//Create either a key=value based list of parameters or json
func generatePluginConfig(configMap map[string]interface{}, configJSON string) (string, error) {
	if configMap != nil && configJSON != "" {
		return "", fmt.Errorf("Cannot declare both config and config_json")
	}
	if configMap != nil {
		var buffer bytes.Buffer
		mapSize := len(configMap)
		position := 1
		for key, value := range configMap {
			buffer.WriteString(key)
			buffer.WriteString("=")
			buffer.WriteString(value.(string))
			if mapSize > 1 && position != mapSize {
				buffer.WriteString("&")
			}
			position = position + 1
		}
		return buffer.String(), nil
	}
	return configJSON, nil
}

func resourceKongConsumerPluginConfigCreate(d *schema.ResourceData, meta interface{}) error {

	consumerId := readStringFromResource(d, "consumer_id")
	pluginName := readStringFromResource(d, "plugin_name")
	config, err := generatePluginConfig(readMapFromResource(d, "config"), readStringFromResource(d, "config_json"))
	if err != nil {
		return fmt.Errorf("error configuring plugin: %v", err)
	}
	consumerPluginConfig, err := meta.(*gokong.KongAdminClient).Consumers().CreatePluginConfig(consumerId, pluginName, config)
	if err != nil {
		return fmt.Errorf("failed to create kong consumer plugin config, error: %v", err)
	}

	if consumerPluginConfig == nil {
		d.SetId("")
	} else {
		d.SetId(buildId(consumerId, pluginName, consumerPluginConfig.Id))
	}

	return resourceKongConsumerPluginConfigRead(d, meta)
}

func resourceKongConsumerPluginConfigRead(d *schema.ResourceData, meta interface{}) error {

	idFields, err := splitIdIntoFields(d.Id())

	if err != nil {
		return err
	}

	consumerPluginConfig, err := meta.(*gokong.KongAdminClient).Consumers().GetPluginConfig(idFields.consumerId, idFields.pluginName, idFields.id)

	if err != nil {
		return fmt.Errorf("could not find kong consumer plugin config with id: %s error: %v", d.Id(), err)
	}

	if consumerPluginConfig == nil {
		return fmt.Errorf("could not configure plugin for kong consumer")
	}

	d.Set("consumer_id", idFields.consumerId)
	d.Set("plugin_name", idFields.pluginName)

	// We sync this property from upstream as a method to allow you to import a resource with the config tracked in
	// terraform state. We do not track `config` as it will be a source of a perpetual diff.
	// https://www.terraform.io/docs/extend/best-practices/detecting-drift.html#capture-all-state-in-read
	upstreamJson, err := consumerPluginConfigJsonToString(consumerPluginConfig.Body)
	if err != nil {
		return fmt.Errorf("could not read in consumer plugin config body: %s error: %v", d.Id(), err)
	}

	d.Set("config_json", upstreamJson)

	return nil
}

func resourceKongConsumerPluginConfigDelete(d *schema.ResourceData, meta interface{}) error {

	idFields, err := splitIdIntoFields(d.Id())

	if err != nil {
		return err
	}

	err = meta.(*gokong.KongAdminClient).Consumers().DeletePluginConfig(idFields.consumerId, idFields.pluginName, idFields.id)

	if err != nil {
		return fmt.Errorf("could not delete kong consumer plugin config: %v", err)
	}

	return nil
}

// Since this config is a schemaless "blob" we have to remove computed properties
func consumerPluginConfigJsonToString(body string) (string, error) {
	data := map[string]interface{}{}
	marshalledData := map[string]interface{}{}
	err := json.Unmarshal([]byte(body), &data)
	if err != nil {
		return "", err
	}

	for key, val := range data {
		if !contains(computedPluginProperties, key) {
			marshalledData[key] = val
		}
	}
	rawJson, _ := json.Marshal(marshalledData)

	return string(rawJson), nil
}
