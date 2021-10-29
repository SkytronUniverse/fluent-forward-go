package ws_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/IBM/fluent-forward-go/fluent/client/ws"
	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

type message struct {
	mt  int
	msg []byte
	err error
}

var _ = Describe("Connection", func() {

	// Describe("NewConnection", func() {
	// 	It("works", func() {
	// 		c, err := ws.NewConnection(fakeConn, ws.ConnectionOptions{})
	// 		Expect(err).ToNot(HaveOccurred())
	// 		Expect(c).ToNot(BeNil())
	// 		// TODO: test options are set correctly
	// 	})
	// })

	var (
		checkSvrClose               bool
		connection, svrConnection   ws.Connection
		svr                         *httptest.Server
		opts                        *ws.ConnectionOptions
		svrRcvdMsgs, clientRcvdMsgs chan message
		listenErrs                  chan error
	)

	var makeOpts = func(msgChan chan message, name string) *ws.ConnectionOptions {
		return &ws.ConnectionOptions{
			CloseDeadline: 500 * time.Millisecond,
			ReadHandler: func(conn ws.Connection, msgType int, p []byte, err error) error {
				msg := message{
					mt:  msgType,
					msg: p,
					err: err,
				}
				msgChan <- msg

				if err != nil {
					log.Println(name, "ReadHandler received error:", err)
				}

				return err
			},
		}
	}

	newHandler := func(svrRcvdMsgs chan message) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			svrOpts := makeOpts(svrRcvdMsgs, "server")
			svrOpts.ID = "server"

			var upgrader websocket.Upgrader
			wc, _ := upgrader.Upgrade(w, r, nil)

			var err error
			svrConnection, err = ws.NewConnection(wc, svrOpts)
			if err != nil {
				return
			}

			Expect(svrConnection.Listen()).ToNot(HaveOccurred())
			log.Println("exit server handler")
		})
	}

	BeforeEach(func() {
		checkSvrClose = true
		svrRcvdMsgs = make(chan message)
		svr = httptest.NewServer(newHandler(svrRcvdMsgs))

		clientRcvdMsgs = make(chan message, 1)
		opts = makeOpts(clientRcvdMsgs, "client")
		opts.ID = "client"

		u := "ws" + strings.TrimPrefix(svr.URL, "http")
		conn, _, err := websocket.DefaultDialer.Dial(u, nil)
		Expect(err).ToNot(HaveOccurred())

		connection, err = ws.NewConnection(conn, opts)
		Expect(err).ToNot(HaveOccurred())

	})

	JustBeforeEach(func() {
		listenErrs = make(chan error)

		go func() {
			defer GinkgoRecover()
			if err := connection.Listen(); err != nil {
				listenErrs <- err
			}
		}()

		// wait for Listen loop to start
		time.Sleep(10 * time.Millisecond)
		Expect(connection.Closed()).To(BeFalse())
	})

	AfterEach(func() {
		if !connection.Closed() {
			Expect(connection.Close()).ToNot(HaveOccurred())
			Eventually(connection.Closed).Should(BeTrue())
		}

		if !svrConnection.Closed() {
			err := svrConnection.Close()
			if checkSvrClose {
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(svrConnection.Closed).Should(BeTrue())
		}

		svr.Close()
	})

	Describe("WriteMessage", func() {
		When("everything is copacetic", func() {
			It("writes messages to the connection", func() {
				err := connection.WriteMessage(1, []byte("oi"))
				Expect(err).ToNot(HaveOccurred())
				err = connection.WriteMessage(1, []byte("koi"))
				Expect(err).ToNot(HaveOccurred())

				m := <-svrRcvdMsgs
				Expect(m.msg).To(Equal([]byte("oi")))
				m = <-svrRcvdMsgs
				Expect(m.msg).To(Equal([]byte("koi")))

				Consistently(svrRcvdMsgs).ShouldNot(Receive())
			})
		})

		When("an error occurs", func() {
			It("returns an error", func() {
				Expect(connection.Close()).ToNot(HaveOccurred())
				Expect(connection.WriteMessage(1, nil).Error()).To(MatchRegexp("close sent"))
			})
		})
	})

	Describe("Listen", func() {
		When("everything is copacetic", func() {
			It("reads a message from the connection and calls the read handler", func() {
				Expect(len(svrRcvdMsgs)).To(Equal(0))

				err := connection.WriteMessage(1, []byte("oi"))
				Expect(err).ToNot(HaveOccurred())

				m := <-svrRcvdMsgs
				Expect(m.err).ToNot(HaveOccurred())
				Expect(bytes.Equal(m.msg, []byte("oi"))).To(BeTrue())

				Consistently(svrRcvdMsgs).ShouldNot(Receive())
			})
		})

		When("already listening", func() {
			It("errors", func() {
				Expect(connection.Listen().Error()).To(MatchRegexp("already listening on this connection"))
			})
		})

		When("an error occurs", func() {
			It("enqueues the error", func() {
				err := svrConnection.CloseWithMsg(websocket.ClosePolicyViolation, "meh")
				Expect(err).ToNot(HaveOccurred())
				err = <-listenErrs
				Expect(err.Error()).To(MatchRegexp("meh"))
			}, 5)

			When("the error is a normal close", func() {
				It("does not enqueue the error", func() {
					Expect(svrConnection.Close()).ToNot(HaveOccurred())
					Consistently(listenErrs).ShouldNot(Receive())
				})
			})
		})
	})

	Describe("CloseWithMsg", func() {
		When("everything is copacetic", func() {
			It("sends a signal", func() {
				Expect(connection.CloseWithMsg(1000, "oi")).ToNot(HaveOccurred())
				Expect(connection.Closed()).To(BeTrue())

				closeMsg := <-svrRcvdMsgs
				Expect(closeMsg.err.Error()).To(MatchRegexp("oi"))
			})
		})
	})

	Describe("Close and Closed", func() {
		JustBeforeEach(func() {
			Expect(connection.Closed()).To(BeFalse())
		})

		AfterEach(func() {
			Expect(connection.Closed()).To(BeTrue())
		})

		When("everything is copacetic", func() {
			It("signals close", func() {
				Expect(connection.Close()).ToNot(HaveOccurred())
				closeMsg := <-svrRcvdMsgs
				Expect(closeMsg.err.Error()).To(MatchRegexp("closing connection"))
			})
		})

		When("called multiple times", func() {
			It("errors", func() {
				Expect(connection.Close()).ToNot(HaveOccurred())
				Expect(connection.Close().Error()).To(MatchRegexp("multiple close calls"))
			})
		})

		When("the connection errors on close", func() {
			BeforeEach(func() {
				checkSvrClose = false
			})

			It("returns an error", func() {
				connection.UnderlyingConn().Close()
				Expect(connection.Close().Error()).To(MatchRegexp("use of closed network connection"))
			})
		})
	})
})
