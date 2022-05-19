package guardiand

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/certusone/wormhole/node/pkg/alephium"
	"github.com/certusone/wormhole/node/pkg/db"
	gossipv1 "github.com/certusone/wormhole/node/pkg/proto/gossip/v1"
	publicrpcv1 "github.com/certusone/wormhole/node/pkg/proto/publicrpc/v1"
	"github.com/certusone/wormhole/node/pkg/publicrpc"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/certusone/wormhole/node/pkg/common"
	nodev1 "github.com/certusone/wormhole/node/pkg/proto/node/v1"
	"github.com/certusone/wormhole/node/pkg/supervisor"
	"github.com/certusone/wormhole/node/pkg/vaa"
)

type nodePrivilegedService struct {
	nodev1.UnimplementedNodePrivilegedServiceServer
	db           *db.Database
	alphDb       *alephium.Database
	injectC      chan<- *vaa.VAA
	obsvReqSendC chan *gossipv1.ObservationRequest
	logger       *zap.Logger
	signedInC    chan *gossipv1.SignedVAAWithQuorum
}

// adminGuardianSetUpdateToVAA converts a nodev1.GuardianSetUpdate message to its canonical VAA representation.
// Returns an error if the data is invalid.
func adminGuardianSetUpdateToVAA(req *nodev1.GuardianSetUpdate, guardianSetIndex uint32, nonce uint32, sequence uint64) (*vaa.VAA, error) {
	if len(req.Guardians) == 0 {
		return nil, errors.New("empty guardian set specified")
	}

	if len(req.Guardians) > common.MaxGuardianCount {
		return nil, fmt.Errorf("too many guardians - %d, maximum is %d", len(req.Guardians), common.MaxGuardianCount)
	}

	addrs := make([]ethcommon.Address, len(req.Guardians))
	for i, g := range req.Guardians {
		if !ethcommon.IsHexAddress(g.Pubkey) {
			return nil, fmt.Errorf("invalid pubkey format at index %d (%s)", i, g.Name)
		}

		ethAddr := ethcommon.HexToAddress(g.Pubkey)
		for j, pk := range addrs {
			if pk == ethAddr {
				return nil, fmt.Errorf("duplicate pubkey at index %d (duplicate of %d): %s", i, j, g.Name)
			}
		}

		addrs[i] = ethAddr
	}

	v := vaa.CreateGovernanceVAA(nonce, sequence, guardianSetIndex,
		vaa.BodyGuardianSetUpdate{
			Keys:     addrs,
			NewIndex: guardianSetIndex + 1,
		}.Serialize())

	return v, nil
}

// adminContractUpgradeToVAA converts a nodev1.ContractUpgrade message to its canonical VAA representation.
// Returns an error if the data is invalid.
func adminContractUpgradeToVAA(req *nodev1.ContractUpgrade, guardianSetIndex uint32, nonce uint32, sequence uint64) (*vaa.VAA, error) {
	b, err := hex.DecodeString(req.NewContract)
	if err != nil {
		return nil, errors.New("invalid new contract address encoding (expected hex)")
	}

	if len(b) != 32 {
		return nil, errors.New("invalid new_contract address")
	}

	if req.ChainId > math.MaxUint16 {
		return nil, errors.New("invalid chain_id")
	}

	newContractAddress := vaa.Address{}
	copy(newContractAddress[:], b)

	v := vaa.CreateGovernanceVAA(nonce, sequence, guardianSetIndex,
		vaa.BodyContractUpgrade{
			ChainID:     vaa.ChainID(req.ChainId),
			NewContract: newContractAddress,
		}.Serialize())

	return v, nil
}

// tokenBridgeRegisterChain converts a nodev1.TokenBridgeRegisterChain message to its canonical VAA representation.
// Returns an error if the data is invalid.
func tokenBridgeRegisterChain(req *nodev1.BridgeRegisterChain, guardianSetIndex uint32, nonce uint32, sequence uint64) (*vaa.VAA, error) {
	if req.ChainId > math.MaxUint16 {
		return nil, errors.New("invalid chain_id")
	}

	b, err := hex.DecodeString(req.EmitterAddress)
	if err != nil {
		return nil, errors.New("invalid emitter address encoding (expected hex)")
	}

	if len(b) != 32 {
		return nil, errors.New("invalid emitter address (expected 32 bytes)")
	}

	emitterAddress := vaa.Address{}
	copy(emitterAddress[:], b)

	v := vaa.CreateGovernanceVAA(nonce, sequence, guardianSetIndex,
		vaa.BodyTokenBridgeRegisterChain{
			Module:         req.Module,
			ChainID:        vaa.ChainID(req.ChainId),
			EmitterAddress: emitterAddress,
		}.Serialize())

	return v, nil
}

// tokenBridgeUpgradeContract converts a nodev1.TokenBridgeRegisterChain message to its canonical VAA representation.
// Returns an error if the data is invalid.
func tokenBridgeUpgradeContract(req *nodev1.BridgeUpgradeContract, guardianSetIndex uint32, nonce uint32, sequence uint64) (*vaa.VAA, error) {
	if req.TargetChainId > math.MaxUint16 {
		return nil, errors.New("invalid target_chain_id")
	}

	b, err := hex.DecodeString(req.NewContract)
	if err != nil {
		return nil, errors.New("invalid new contract address (expected hex)")
	}

	if len(b) != 32 {
		return nil, errors.New("invalid new contract address (expected 32 bytes)")
	}

	newContract := vaa.Address{}
	copy(newContract[:], b)

	v := vaa.CreateGovernanceVAA(nonce, sequence, guardianSetIndex,
		vaa.BodyTokenBridgeUpgradeContract{
			Module:        req.Module,
			TargetChainID: vaa.ChainID(req.TargetChainId),
			NewContract:   newContract,
		}.Serialize())

	return v, nil
}

func tokenBridgeUndoneTransfer(req *nodev1.TokenBridgeUndoneTransfer, guardianSetIndex uint32, nonce uint32, sequence uint64) (*vaa.VAA, error) {
	v := vaa.CreateGovernanceVAA(nonce, sequence, guardianSetIndex, req.Payload)
	v.ConsistencyLevel = uint8(req.ConsistencyLevel)
	return v, nil
}

func (s *nodePrivilegedService) InjectGovernanceVAA(ctx context.Context, req *nodev1.InjectGovernanceVAARequest) (*nodev1.InjectGovernanceVAAResponse, error) {
	s.logger.Info("governance VAA injected via admin socket", zap.String("request", req.String()))

	var (
		v   *vaa.VAA
		err error
	)

	digests := make([][]byte, len(req.Messages))

	for i, message := range req.Messages {
		switch payload := message.Payload.(type) {
		case *nodev1.GovernanceMessage_GuardianSet:
			v, err = adminGuardianSetUpdateToVAA(payload.GuardianSet, req.CurrentSetIndex, message.Nonce, message.Sequence)
		case *nodev1.GovernanceMessage_ContractUpgrade:
			v, err = adminContractUpgradeToVAA(payload.ContractUpgrade, req.CurrentSetIndex, message.Nonce, message.Sequence)
		case *nodev1.GovernanceMessage_BridgeRegisterChain:
			v, err = tokenBridgeRegisterChain(payload.BridgeRegisterChain, req.CurrentSetIndex, message.Nonce, message.Sequence)
		case *nodev1.GovernanceMessage_BridgeContractUpgrade:
			v, err = tokenBridgeUpgradeContract(payload.BridgeContractUpgrade, req.CurrentSetIndex, message.Nonce, message.Sequence)
		case *nodev1.GovernanceMessage_UndoneTransfer:
			v, err = tokenBridgeUndoneTransfer(payload.UndoneTransfer, req.CurrentSetIndex, message.Nonce, message.Sequence)
		default:
			panic(fmt.Sprintf("unsupported VAA type: %T", payload))
		}
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}

		// Generate digest of the unsigned VAA.
		digest := v.SigningMsg()

		s.logger.Info("governance VAA constructed",
			zap.Any("vaa", v),
			zap.String("digest", digest.String()),
		)

		s.injectC <- v

		digests[i] = digest.Bytes()
	}

	return &nodev1.InjectGovernanceVAAResponse{Digests: digests}, nil
}

// fetchMissing attempts to backfill a gap by fetching and storing missing signed VAAs from the network.
// Returns true if the gap was filled, false otherwise.
func (s *nodePrivilegedService) fetchMissing(
	ctx context.Context,
	nodes []string,
	c *http.Client,
	chain vaa.ChainID,
	addr string,
	seq uint64) (bool, error) {

	// shuffle the list of public RPC endpoints
	rand.Shuffle(len(nodes), func(i, j int) {
		nodes[i], nodes[j] = nodes[j], nodes[i]
	})

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	for _, node := range nodes {
		req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf(
			"%s/v1/signed_vaa/%d/%s/%d", node, chain, addr, seq), nil)
		if err != nil {
			return false, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := c.Do(req)
		if err != nil {
			s.logger.Warn("failed to fetch missing VAA",
				zap.String("node", node),
				zap.String("chain", chain.String()),
				zap.String("address", addr),
				zap.Uint64("sequence", seq),
				zap.Error(err),
			)
			continue
		}

		switch resp.StatusCode {
		case http.StatusNotFound:
			resp.Body.Close()
			continue
		case http.StatusOK:
			type getVaaResp struct {
				VaaBytes string `json:"vaaBytes"`
			}
			var respBody getVaaResp
			if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
				resp.Body.Close()
				s.logger.Warn("failed to decode VAA response",
					zap.String("node", node),
					zap.String("chain", chain.String()),
					zap.String("address", addr),
					zap.Uint64("sequence", seq),
					zap.Error(err),
				)
				continue
			}

			// base64 decode the VAA bytes
			vaaBytes, err := base64.StdEncoding.DecodeString(respBody.VaaBytes)
			if err != nil {
				resp.Body.Close()
				s.logger.Warn("failed to decode VAA body",
					zap.String("node", node),
					zap.String("chain", chain.String()),
					zap.String("address", addr),
					zap.Uint64("sequence", seq),
					zap.Error(err),
				)
				continue
			}

			s.logger.Info("backfilled VAA",
				zap.Uint16("chain", uint16(chain)),
				zap.String("address", addr),
				zap.Uint64("sequence", seq),
				zap.Int("numBytes", len(vaaBytes)),
			)

			// Inject into the gossip signed VAA receive path.
			// This has the same effect as if the VAA was received from the network
			// (verifying signature, publishing to BigTable, storing in local DB...).
			s.signedInC <- &gossipv1.SignedVAAWithQuorum{
				Vaa: vaaBytes,
			}

			resp.Body.Close()
			return true, nil
		default:
			resp.Body.Close()
			return false, fmt.Errorf("unexpected response status: %d", resp.StatusCode)
		}
	}

	return false, nil
}

func (s *nodePrivilegedService) FindMissingMessages(ctx context.Context, req *nodev1.FindMissingMessagesRequest) (*nodev1.FindMissingMessagesResponse, error) {
	b, err := hex.DecodeString(req.EmitterAddress)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid emitter address encoding: %v", err)
	}
	emitterAddress := vaa.Address{}
	copy(emitterAddress[:], b)

	ids, first, last, err := s.db.FindEmitterSequenceGap(db.VAAID{
		EmitterChain:   vaa.ChainID(req.EmitterChain),
		EmitterAddress: emitterAddress,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "database operation failed: %v", err)
	}

	if req.RpcBackfill {
		c := &http.Client{}
		unfilled := make([]uint64, 0, len(ids))
		for _, id := range ids {
			if ok, err := s.fetchMissing(ctx, req.BackfillNodes, c, vaa.ChainID(req.EmitterChain), emitterAddress.String(), id); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to backfill VAA: %v", err)
			} else if ok {
				continue
			}
			unfilled = append(unfilled, id)
		}
		ids = unfilled
	}

	resp := make([]string, len(ids))
	for i, v := range ids {
		resp[i] = fmt.Sprintf("%d/%s/%d", req.EmitterChain, emitterAddress, v)
	}
	return &nodev1.FindMissingMessagesResponse{
		MissingMessages: resp,
		FirstSequence:   first,
		LastSequence:    last,
	}, nil
}

func adminServiceRunnable(
	logger *zap.Logger,
	socketPath string,
	injectC chan<- *vaa.VAA,
	signedInC chan *gossipv1.SignedVAAWithQuorum,
	obsvReqSendC chan *gossipv1.ObservationRequest,
	db *db.Database,
	alphDb *alephium.Database,
	gst *common.GuardianSetState,
) (supervisor.Runnable, error) {
	// Delete existing UNIX socket, if present.
	fi, err := os.Stat(socketPath)
	if err == nil {
		fmode := fi.Mode()
		if fmode&os.ModeType == os.ModeSocket {
			err = os.Remove(socketPath)
			if err != nil {
				return nil, fmt.Errorf("failed to remove existing socket at %s: %w", socketPath, err)
			}
		} else {
			return nil, fmt.Errorf("%s is not a UNIX socket", socketPath)
		}
	}

	// Create a new UNIX socket and listen to it.

	// The socket is created with the default umask. We set a restrictive umask in setRestrictiveUmask
	// to ensure that any files we create are only readable by the user - this is much harder to mess up.
	// The umask avoids a race condition between file creation and chmod.

	laddr, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address: %v", err)
	}
	l, err := net.ListenUnix("unix", laddr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", socketPath, err)
	}

	logger.Info("admin server listening on", zap.String("path", socketPath))

	nodeService := &nodePrivilegedService{
		injectC:      injectC,
		obsvReqSendC: obsvReqSendC,
		db:           db,
		alphDb:       alphDb,
		logger:       logger.Named("adminservice"),
		signedInC:    signedInC,
	}

	publicrpcService := publicrpc.NewPublicrpcServer(logger, db, gst)

	grpcServer := common.NewInstrumentedGRPCServer(logger)
	nodev1.RegisterNodePrivilegedServiceServer(grpcServer, nodeService)
	publicrpcv1.RegisterPublicRPCServiceServer(grpcServer, publicrpcService)
	return supervisor.GRPCServer(grpcServer, l, false), nil
}

func (s *nodePrivilegedService) SendObservationRequest(ctx context.Context, req *nodev1.SendObservationRequestRequest) (*nodev1.SendObservationRequestResponse, error) {
	s.obsvReqSendC <- req.ObservationRequest
	s.logger.Info("sent observation request", zap.Any("request", req.ObservationRequest))
	return &nodev1.SendObservationRequestResponse{}, nil
}

func (s *nodePrivilegedService) GetUndoneSequences(ctx context.Context, req *nodev1.GetUndoneSequencesRequest) (*nodev1.GetUndoneSequencesResponse, error) {
	sequences, err := s.alphDb.GetUndoneSequences(uint16(req.RemoteChainId))
	if err != nil {
		return nil, err
	}
	return &nodev1.GetUndoneSequencesResponse{
		Sequences: sequences,
	}, nil
}

func (s *nodePrivilegedService) tryToGetVAA(vaaId *db.VAAID) ([]byte, error) {
	maxTimes := 15
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	times := 0
	for {
		select {
		case <-ticker.C:
			times += 1
			if times > maxTimes {
				return nil, errors.New("failed to get vaa after fetched from remote guardian")
			}

			vaaBytes, err := s.db.GetSignedVAABytes(*vaaId)
			if err == nil {
				return vaaBytes, nil
			}

			if err == db.ErrVAANotFound {
				continue
			}

			if err != nil {
				return nil, err
			}
		}
	}
}

type transfer struct {
	amount       big.Int
	tokenId      alephium.Byte32
	tokenChainId uint16
	toAddress    alephium.Byte32
	toChainId    uint16
	fee          big.Int
}

func readBigInt(reader *bytes.Reader, num *big.Int) error {
	bs := make([]byte, 32)
	size, err := reader.Read(bs)
	if err != nil {
		return err
	}
	if size != 32 {
		return errors.New("read bigint EOF")
	}
	num.SetBytes(bs)
	return nil
}

func readUint16(reader *bytes.Reader, num *uint16) error {
	return binary.Read(reader, binary.BigEndian, num)
}

func readByte32(reader *bytes.Reader, byte32 *alephium.Byte32) error {
	size, err := reader.Read(byte32[:])
	if err != nil {
		return err
	}
	if size != 32 {
		return errors.New("read byte32 EOF")
	}
	return nil
}

func transferFromBytes(data []byte) (*transfer, error) {
	if data[0] != byte(1) {
		return nil, errors.New("invalid payload id, expect transfer vaa")
	}

	reader := bytes.NewReader(data[1:]) // skip the payloadId
	var message transfer
	var err error

	if err = readBigInt(reader, &message.amount); err != nil {
		return nil, err
	}
	if err = readByte32(reader, &message.tokenId); err != nil {
		return nil, err
	}
	if err = readUint16(reader, &message.tokenChainId); err != nil {
		return nil, err
	}
	if err = readByte32(reader, &message.toAddress); err != nil {
		return nil, err
	}
	if err = readUint16(reader, &message.toChainId); err != nil {
		return nil, err
	}
	if err = readBigInt(reader, &message.fee); err != nil {
		return nil, err
	}
	return &message, nil
}

func (s *nodePrivilegedService) getTokenWrapperId(msg *transfer, remoteChainId uint16) (*alephium.Byte32, error) {
	if msg.tokenChainId == uint16(vaa.ChainIDAlephium) {
		// local token
		return s.alphDb.GetLocalTokenWrapper(msg.tokenId, remoteChainId)
	} else {
		// remote token
		return s.alphDb.GetRemoteTokenWrapper(msg.tokenId)
	}
}

func (s *nodePrivilegedService) GenUndoneTransferGovernanceMsg(ctx context.Context, req *nodev1.GenUndoneTransferGovernanceMsgRequest) (*nodev1.GenUndoneTransferGovernanceMsgResponse, error) {
	address, err := vaa.StringToAddress(req.EmitterAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid emitter address, error: %v", err)
	}

	emitterChain := vaa.ChainID(req.EmitterChain)
	vaaId := db.VAAID{
		EmitterChain:   emitterChain,
		EmitterAddress: address,
		Sequence:       req.Sequence,
	}
	vaaBytes, err := s.db.GetSignedVAABytes(vaaId)
	if err != nil && err != db.ErrVAANotFound {
		return nil, fmt.Errorf("failed to get vaa from local storage, error: %v", err)
	}

	if err == db.ErrVAANotFound {
		client := &http.Client{}
		succeed, err := s.fetchMissing(ctx, req.BackfillNodes, client, emitterChain, req.EmitterAddress, req.Sequence)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch vaa from remote guardians, error: %v", err)
		}

		if !succeed {
			return nil, fmt.Errorf("failed to fetch vaa from remote guardians, try other guardians")
		}

		vaaBytes, err = s.tryToGetVAA(&vaaId)
		if err != nil {
			return nil, err
		}
	}

	transferVAA, err := vaa.Unmarshal(vaaBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal vaa, error: %v", err)
	}

	transferMsg, err := transferFromBytes(transferVAA.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode transfer message, error: %v", err)
	}

	tokenWrapperId, err := s.getTokenWrapperId(transferMsg, uint16(req.EmitterChain))
	if err != nil {
		return nil, fmt.Errorf("failed to get token wrapper id, error: %v", err)
	}

	// TODO: save the vaa id
	if err := s.alphDb.SetSequenceExecuting(uint16(emitterChain), req.Sequence); err != nil {
		return nil, fmt.Errorf("failed to update undone sequence status, error: %v", err)
	}
	payload := &nodev1.GovernanceMessage_UndoneTransfer{
		UndoneTransfer: &nodev1.TokenBridgeUndoneTransfer{
			ConsistencyLevel: uint32(transferVAA.ConsistencyLevel),
			Payload:          transferPayload(transferMsg, tokenWrapperId, req.Sequence),
		},
	}
	return &nodev1.GenUndoneTransferGovernanceMsgResponse{
		Msg: &nodev1.GovernanceMessage{
			Sequence: req.GovSequence,
			Nonce:    rand.Uint32(),
			Payload:  payload,
		},
	}, nil
}

// TODO: better name
// transfer payload for undone sequence
func transferPayload(
	transferMsg *transfer,
	tokenWrapperId *alephium.Byte32,
	sequence uint64,
) []byte {
	buf := new(bytes.Buffer)
	buf.Write(vaa.TokenBridgeModule)
	buf.WriteByte(3) // action id
	binary.Write(buf, binary.BigEndian, sequence)
	buf.Write(tokenWrapperId[:])
	buf.Write(transferMsg.toAddress[:])
	buf.Write(transferMsg.amount.FillBytes(make([]byte, 32)))
	buf.Write(transferMsg.fee.FillBytes(make([]byte, 32)))
	return buf.Bytes()
}
