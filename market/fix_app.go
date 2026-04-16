package market

import (
	"fmt"
	"log"

	"github.com/quickfixgo/quickfix"
	"github.com/quickfixgo/quickfix/enum"
	"github.com/quickfixgo/quickfix/field"
	"github.com/quickfixgo/quickfix/tag"
	"github.com/quickfixgo/quickfix/fix44/marketdatarequest"
	"github.com/quickfixgo/quickfix/fix44/marketdatasnapshotfullrefresh"
	"pricing-service/engine"
)

type FIXApplication struct {
	calculator *engine.Calculator
	SessionID  quickfix.SessionID
	username   string
	password   string
}

func NewFIXApplication(calc *engine.Calculator, username string, password string) *FIXApplication {
	return &FIXApplication{
		calculator: calc,
		username:   username,
		password:   password,
	}
}

// OnCreate implemented as part of quickfix.Application interface
func (f *FIXApplication) OnCreate(sessionID quickfix.SessionID) {
	f.SessionID = sessionID
	log.Printf("FIX Session created: %s\n", sessionID.String())
}

// OnLogon implemented as part of quickfix.Application interface
func (f *FIXApplication) OnLogon(sessionID quickfix.SessionID) {
	log.Printf("FIX Session logged on: %s\n", sessionID.String())

	// Subscribe to predefined instruments
	// In production, you might fetch this list from the database.
	symbols := []string{
		"AUDCAD", "AUDCHF", "AUDJPY", "AUDNZD", "AUDSGD", "AUDUSD",
		"BTCUSD", "CADCHF", "CADJPY", "CHFJPY", "CHFSGD", "EURAUD",
		"EURCAD", "EURCHF", "EURCZK", "EURGBP", "EURJPY", "EURNOK",
		"EURNZD", "EURPLN", "EURSEK", "EURSGD", "EURTRY", "EURUSD",
		"GBPAUD", "GBPCAD", "GBPCHF", "GBPJPY", "GBPNZD", "GBPSGD",
		"GBPUSD", "NZDCAD", "NZDCHF", "NZDJPY", "NZDUSD", "USDCAD",
		"USDCHF", "USDCNH", "USDCZK", "USDDKK", "USDHKD", "USDHUF",
		"USDJPY", "USDMXN", "USDNOK", "USDPLN", "USDSEK", "USDSGD",
		"USDTRY", "USDZAR", "XAGUSD", "XAUUSD", "UKOIL", "USOIL",
		// Indices could be added if needed
	}

	for i, sym := range symbols {
		request := marketdatarequest.New(
			field.NewMDReqID(fmt.Sprintf("REQ%d", i)),
			field.NewSubscriptionRequestType(enum.SubscriptionRequestType_SNAPSHOT_PLUS_UPDATES),
			field.NewMarketDepth(1), // Use 1 for top of book (Python used 1 implicitly vs 0)
		)

		request.Set(field.NewMDUpdateType(enum.MDUpdateType_FULL_REFRESH))

		groupType := marketdatarequest.NewNoMDEntryTypesRepeatingGroup()
		groupType.Add().SetMDEntryType(enum.MDEntryType_BID)
		groupType.Add().SetMDEntryType(enum.MDEntryType_OFFER)
		request.SetNoMDEntryTypes(groupType)

		symbolsGroup := marketdatarequest.NewNoRelatedSymRepeatingGroup()
		symbolsGroup.Add().SetSymbol(sym)
		request.SetNoRelatedSym(symbolsGroup)

		err := quickfix.SendToTarget(request, sessionID)
		if err != nil {
			log.Printf("Error sending Market Data Request for %s: %v\n", sym, err)
		}
	}
}

// OnLogout implemented as part of quickfix.Application interface
func (f *FIXApplication) OnLogout(sessionID quickfix.SessionID) {
	log.Printf("FIX Session logged out: %s\n", sessionID.String())
}

// ToAdmin implemented as part of quickfix.Application interface
func (f *FIXApplication) ToAdmin(msg *quickfix.Message, sessionID quickfix.SessionID) {
	msgType, err := msg.Header.GetString(tag.MsgType)
	if err == nil && msgType == string(enum.MsgType_LOGON) {
		if f.username != "" {
			msg.Body.SetString(tag.Username, f.username)
		}
		if f.password != "" {
			msg.Body.SetString(tag.Password, f.password)
		}
	}
}

// ToApp implemented as part of quickfix.Application interface
func (f *FIXApplication) ToApp(msg *quickfix.Message, sessionID quickfix.SessionID) error {
	return nil
}

// FromAdmin implemented as part of quickfix.Application interface
func (f *FIXApplication) FromAdmin(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	return nil
}

// FromApp receives messages from the FIX server.
// Handles MarketDataSnapshotFullRefresh (W) and sends MassQuoteAck for tag 117
// to keep the LP price stream alive (matches Python LP connection logic).
func (f *FIXApplication) FromApp(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	msgType, err := msg.Header.GetString(tag.MsgType)
	if err != nil {
		return nil
	}

	// We only care about W (MarketDataSnapshotFullRefresh)
	if msgType == string(enum.MsgType_MARKET_DATA_SNAPSHOT_FULL_REFRESH) {
		f.handleMarketData(msg)
		// If QuoteID (tag 117) is present, respond with MassQuoteAcknowledgement
		// This is critical: PrimeXM expects an ACK and stops sending prices without it.
		f.sendMassQuoteAckIfPresent(msg, sessionID)
	}

	return nil
}

// sendMassQuoteAckIfPresent checks for QuoteID (tag 117) and responds with
// a MassQuoteAcknowledgement (35=b), matching the Python LP connection logic.
func (f *FIXApplication) sendMassQuoteAckIfPresent(msg *quickfix.Message, sessionID quickfix.SessionID) {
	quoteID, err := msg.Body.GetString(tag.QuoteID)
	if err != nil {
		return // No QuoteID present, nothing to ack
	}

	ack := quickfix.NewMessage()
	ack.Header.SetString(tag.MsgType, "b") // MassQuoteAcknowledgement
	ack.Body.SetString(tag.QuoteID, quoteID)
	ack.Body.SetInt(tag.QuoteStatus, 0) // Accepted

	if sendErr := quickfix.SendToTarget(ack, sessionID); sendErr != nil {
		log.Printf("Failed to send MassQuoteAck for QuoteID=%s: %v", quoteID, sendErr)
	}
}

func (f *FIXApplication) handleMarketData(msg *quickfix.Message) {
	refresh := marketdatasnapshotfullrefresh.FromMessage(msg)
	
	symbol, err := refresh.GetSymbol()
	if err != nil {
		return
	}

	entries, err := refresh.GetNoMDEntries()
	if err != nil {
		return
	}

	var bid, ask float64

	for i := 0; i < entries.Len(); i++ {
		entry := entries.Get(i)
		
		entryType, err := entry.GetMDEntryType()
		if err != nil {
			continue
		}

		price, err := entry.GetMDEntryPx()
		if err != nil {
			continue
		}
        priceF, _ := price.Float64()

		if entryType == enum.MDEntryType_BID {
			bid = priceF
		} else if entryType == enum.MDEntryType_OFFER {
			ask = priceF
		}
	}

	if bid > 0 && ask > 0 {
		// Drop tick into isolated memory channel immediately (nanoseconds)
		tick := engine.RawTick{
			Symbol: symbol,
			Bid:    bid,
			Ask:    ask,
		}
		
		select {
		case f.calculator.TickPipe <- tick:
		default:
			// If buffer is full, drop tick. We only care about latest anyway.
		}
	}
}
