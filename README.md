# RabbitMQ Broker for Akkeris


## Run Time Environment

* CLUSTERS: Comma seperated list of rabbitmq clusters. Ex: sandbox,live
* LIVE_RABBIT_AMQP: hostname to access queues in cluster 'live'. See below
* LIVE_RABBIT_URL: hostname to access management api in cluster 'live'.
* RABBITMQ_SECRET: Vault path to secret. Ex: secret/to/rabbitmq-api/admin/secrets
* VAULT_ADDR: Full url to vault. Ex: https://vault.example.io
* VAULT_TOKEN: Access token for vault.
* PORT: Port to access this api.  Default is 3000 (See [Go Martini](https://github.com/go-martini/martini))

### For each cluster

* [CLUSTER_NAME]_RABBIT_URL - Api hostname (can be same as queue hostname)
    * Ex: LIVE_RABBIT_URL=rabbitmq-prod.example.com
    * Ex: SANDBOX_RABBIT_URL=rabbitmq-sandbox-api.example.io
* [CLUSTER_NAME]_RABBIT_AMQP - Queues hostname
    * Ex: LIVE_RABBIT_AMQP=rabbitmq-prod.example.com
    * Ex: SANDBOX_RABBIT_AMQP=rabbitmq-sandbox.example.io

### Vault secret

The vault secret must contain these fields:
* brokerdb: URL for postgress database that stores created rabbitmq users/virtual hosts
    * postgres://username:password@brokerdb.example.io:5432/dbname
* key: 32 byte key used to encrypt rabbitmq user password to store in database
    * "this is thirty-two characters 32"
* username: Username for rabbitmq cluster that has admin privileges 
* password: Password for rabbitmq admin user

## API

### Get list of plans

Get a list of plans that can be used.  Currently these correlate to the cluster names.

**URL** : `/v1/rabbitmq/plans`

**Method** : `GET`

#### Success Response

**Code** : `200 OK`

**Content**


```json
{
    "live":"Prod and real use. Bigger cluster.  Not purged",
    "sandbox":"Dev and Testing and QA and Load testing.  May be purged regularly"
}

```

## Create instance

Create a user and vhost based on plan.

**URL** : `/v1/rabbitmq/instance`

**Method** : `POST`

**Data** All fields required

```json
{
  "plan": "live",
  "billingcode":"MyTeam"
}
```

### Success Response

**Condition** User and Vhost created

**Code** : `201 CREATED`

**Content** 

```json
{
  "RABBITMQ_URL":"amqp://username:password@rabbitmq-sandbox.example.io:5672/username"
}
```

### Error Response >>>**TODO**<<<

**Condition** : Invalid plan or plan missing

**Code** : `400 Bad Request`

**Returned Response** : None

## Get queue connection url for user

**URL** : `/v1/rabbitmq/url/:name`

**Method** : `GET`

### Success Response

**Condition** : User found

**Content** :

```json
{
  "RABBITMQ_URL":"amqp://username:password@rabbitmq-sandbox.example.io:5672/username"
}
```

### Error Response

**Condition** : user not found in database or rabbitmq cluster

**Code** : `404 Not Found`

## Delete user and vhost

**URL** : `/v1/rabbitmq/instance/:username`

**URL Parameters** : `username=[string]`

**Method** : `DELETE`

### Success response

**Condition** : User and Vhost deleted from database and rabbitmq

**Code** : `200 OK`

**Content** : None

### Error response >>>**TODO**<<<

**Condition** : User or vhost not found in DB or Rabbitmq cluster

**Code** : `404 Not Found`

**Content** : None

## Add tag to user in database

**URL** : `/v1/tag`

**Method** : `POST`

**Data** : 
* resource: vhost id
* name: tag name
* value: tag value

```json
{
  "resource":"VHOST",
  "name":"owner",
  "value":"Captain Janeway"
}
```
	
### Success response

**Condition** : Tag added to vhost in database

**Code** : `201 Created`

**Content** :

```json
{
  "response":"tag added"
}
```

### Error response >>>**TODO**<<<

**Condition** : Vhost no in database

**Code** : `404 Not found`