package workerapp

import (
	managerapi "CrackHash/internal/manager-api"
	workerapi "CrackHash/internal/worker-api"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

func ResultTypeToMsg(t int) (string, error) {
	switch t {
	case RESULT_TYPE_TIMEOUT:
		return managerapi.RESULT_TYPE_TIMEOUT, nil
	case RESULT_TYPE_CRACKED:
		return managerapi.RESULT_TYPE_CRACKED, nil
	default:
		return "", fmt.Errorf("unsupported result type: %d", t)
	}
}

func RequestStatusToMsg(t int) (string, error) {
	switch t {
	case REQUEST_STATUS_DONE:
		return workerapi.REQUEST_STATUS_DONE, nil
	case REQUEST_STATUS_IN_PROGRESS:
		return workerapi.REQUEST_STATUS_IN_PROGRESS, nil
	case REQUEST_STATUS_UNKNOWN:
		return workerapi.REQUEST_STATUS_UNKNOWN, nil
	default:
		return "", fmt.Errorf("unsupported result type: %d", t)
	}
}

type WorkerApp struct {
	logger *zap.Logger

	cfg          *WorkerConfig
	taskRegistry *TasksRegistryActor
	NodeId       string

	rabbit *amqp.Connection
	ch     *amqp.Channel

	pingReceived chan struct{}
}

func NewWorkerApp(cfg *WorkerConfig) (*WorkerApp, error) {
	logger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}

	nodeId := uuid.NewString()

	logger = logger.With(zap.String("actor", "workerApp"), zap.String("nodeId", nodeId))

	tr, err := NewTaskRegistryActor(cfg)
	if err != nil {
		return nil, err
	}
	return &WorkerApp{
		logger:       logger,
		cfg:          cfg,
		taskRegistry: tr,
		NodeId:       nodeId,
		rabbit:       nil,
		pingReceived: make(chan struct{}),
	}, nil
}

func (wApp *WorkerApp) waitForCrackHashResult(d *amqp.Delivery, resCh chan WorkDone) {
	result := <-resCh

	if result.Result.ResultType == RESULT_TYPE_CANCELED {
		return
	}

	resMsg, err := ResultTypeToMsg(result.Result.ResultType)
	if err != nil {
		panic(err)
	}

	informResult := managerapi.InformCrackHashResultBody{
		Hash:  result.Hash,
		Range: result.Range,

		ResultType: resMsg,
		Result:     result.Result.Words,
	}

	var buffer bytes.Buffer

	err = json.NewEncoder(&buffer).Encode(informResult)
	if err != nil {
		panic(err)
	}

	wApp.logger.Info("Crack result received", zap.String("result type", resMsg), zap.Strings("data", result.Result.Words))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err = wApp.ch.PublishWithContext(ctx,
		wApp.cfg.RabbitMqResultExchange, // exchange
		"",                              // routing key
		false,                           // mandatory
		false,                           // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType: "text/json",
			Body:        buffer.Bytes(),
		})

	if err != nil {
		d.Nack(false, true)
		return
	}

	d.Ack(false)
}

func (wApp *WorkerApp) processCrackHash(d *amqp.Delivery, req workerapi.CrackHashRequest) {
	resChan, _ := wApp.taskRegistry.CreateCrackHashTask(context.Background(), req.Hash, req.Range)

	go wApp.waitForCrackHashResult(d, resChan)
}

func (wApp *WorkerApp) DoMain(port uint16, rabbit *amqp.Connection) {
	defer wApp.logger.Sync()

	wApp.rabbit = rabbit

	ch, err := wApp.rabbit.Channel()

	if err != nil {
		log.Fatal(err)
	}

	err = ch.Confirm(false)
	if err != nil {
		log.Fatal(err)
	}

	wApp.ch = ch

	q, err := wApp.setupQueue(ch)

	if err != nil {
		log.Fatal(err)
	}

	wApp.consumeTasksFromQueue(ch, q)
}

func (wApp *WorkerApp) setupQueue(ch *amqp.Channel) (*amqp.Queue, error) {
	taskExchangeName := wApp.cfg.RabbitMqTaskExchange

	err := ch.ExchangeDeclare(
		wApp.cfg.RabbitMqResultExchange,
		"direct",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return nil, err
	}

	err = ch.ExchangeDeclare(
		taskExchangeName, // name
		"direct",         // type
		true,             // durable
		false,            // auto-deleted
		false,            // internal
		false,            // no-wait
		nil,              // arguments
	)

	if err != nil {
		return nil, err
	}

	q, err := ch.QueueDeclare(
		wApp.cfg.RabbitMqTaskQueue, // name
		true,                       // durable
		false,                      // delete when unused
		false,                      // exclusive
		false,                      // no-wait
		nil,                        // arguments
	)

	if err != nil {
		return nil, err
	}

	err = ch.QueueBind(
		q.Name,           // queue name
		"",               // routing key
		taskExchangeName, // exchange
		false,
		nil,
	)

	if err != nil {
		return nil, err
	}

	return &q, nil
}

func (wApp *WorkerApp) consumeTasksFromQueue(ch *amqp.Channel, q *amqp.Queue) error {

	for {
		msgs, err := ch.Consume(
			q.Name,
			"",    // consumer
			false, // auto-ack
			false, // exclusive
			false, // no-local
			false, // no-wait
			nil,   // args
		)
		if err != nil {
			continue
		}

		for d := range msgs {
			req := workerapi.CrackHashRequest{}
			decoder := json.NewDecoder(bytes.NewReader(d.Body))
			decoder.Decode(&req)

			go func(d *amqp.Delivery, req workerapi.CrackHashRequest) {
				wApp.processCrackHash(d, req)
			}(&d, req)
		}
	}

	return nil
}
