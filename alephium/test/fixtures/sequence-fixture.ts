import { NodeProvider, addressFromContractId, Contract } from "@alephium/web3";
import { ContractInfo, initAsset, randomAssetAddress, randomContractAddress, randomContractId } from "./wormhole-fixture";

export async function createUndoneSequence(
    parentId: string,
    begin: number,
    sequences: bigint,
    refundAddress: string,
    contractId?: string
): Promise<ContractInfo> {
    const contract = await Contract.fromSource('sequence/undone_sequence.ral')
    const address = typeof contractId === 'undefined' ? randomContractAddress() : addressFromContractId(contractId)
    const initFields = {
        "parentId": parentId,
        "begin": begin,
        "sequences": sequences,
        "refundAddress": refundAddress
    }
    const state = contract.toState(initFields, initAsset, address)
    return new ContractInfo(contract, state, [], address)
}

export async function createSequence(
    next: number,
    next1: bigint,
    next2: bigint,
    refundAddress: string,
    contractId?: string
): Promise<ContractInfo> {
    const address = typeof contractId === 'undefined' ? randomContractAddress() : addressFromContractId(contractId)
    const undoneSequenceTemplate = await createUndoneSequence(
        randomContractId(), 0, 0n, randomAssetAddress()
    )
    const contract = await Contract.fromSource('tests/sequence_test.ral')
    const initField = {
        'next': next,
        'next1': next1,
        'next2': next2,
        'undoneSequenceTemplateId': undoneSequenceTemplate.contractId,
        'refundAddress': refundAddress
    }
    const state = contract.toState(initField, initAsset, address)
    return new ContractInfo(contract, state, undoneSequenceTemplate.states(), address)
}
