import { BuildScriptTx, SingleAddressSigner } from 'alephium-web3'
import { MsgExecuteContract } from "@terra-money/terra.js";
import { ethers, PayableOverrides } from "ethers";
import { isNativeDenom } from "..";
import { Bridge__factory } from "../ethers-contracts";
import { createNonce } from "../utils/createNonce";
import { attestTokenScript } from '../alephium/token_bridge';
import { executeScript } from './utils';

export async function attestFromAlph(
  signer: SingleAddressSigner,
  tokenBridgeId: string,
  tokenId: string,
  payer: string,
  messageFee: bigint,
  consistencyLevel?: number,
  nonce?: string,
  params?: BuildScriptTx
) {
  const nonceHex = (typeof nonce !== "undefined") ? nonce : createNonce().toString('hex')
  const cl = (typeof consistencyLevel !== "undefined") ? consistencyLevel : 10
  const script = attestTokenScript()
  const scriptParams = typeof params === 'undefined' ? {
    templateVariables: {
      payer: payer,
      tokenBridgeId: tokenBridgeId,
      tokenId: tokenId,
      messageFee: messageFee,
      nonce: nonceHex,
      consistencyLevel: cl
    }
  } : params
  return executeScript(signer, script, scriptParams)
}

export async function attestFromEth(
  tokenBridgeAddress: string,
  signer: ethers.Signer,
  tokenAddress: string,
  overrides: PayableOverrides & { from?: string | Promise<string> } = {}
) {
  const bridge = Bridge__factory.connect(tokenBridgeAddress, signer);
  const v = await bridge.attestToken(tokenAddress, createNonce(), overrides);
  const receipt = await v.wait();
  return receipt;
}

export async function attestFromTerra(
  tokenBridgeAddress: string,
  walletAddress: string,
  asset: string
) {
  const nonce = Math.round(Math.random() * 100000);
  const isNativeAsset = isNativeDenom(asset);
  return new MsgExecuteContract(walletAddress, tokenBridgeAddress, {
    create_asset_meta: {
      asset_info: isNativeAsset
        ? {
            native_token: { denom: asset },
          }
        : {
            token: {
              contract_addr: asset,
            },
          },
      nonce: nonce,
    },
  });
}
