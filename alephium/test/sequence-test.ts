import { CliqueClient, Contract } from 'alephium-web3'
import { createSequence } from './fixtures/sequence-fixture'
import { expectAssertionFailed, randomContractAddress } from './fixtures/wormhole-fixture'

describe("test sequence", () => {
    const client = new CliqueClient({baseUrl: `http://127.0.0.1:22973`})
    const allExecuted = (BigInt(1) << BigInt(256)) - 1n

    function sequenceToHex(seq: number): string {
        const buffer = Buffer.allocUnsafe(8)
        buffer.writeBigUInt64BE(BigInt(seq), 0)
        return buffer.toString('hex')
    }

    test("should execute correctly", async () => {
        const sequenceInfo = await createSequence(client, 0, 0n, 0n)
        const sequence = sequenceInfo.contract
        for (let seq = 0; seq < 256; seq++) {
            const testResult = await sequence.testPrivateMethod(client, 'checkSequence', {
                initialFields: sequenceInfo.selfState.fields,
                address: sequenceInfo.address,
                testArgs: [seq]
            })
            // won't load undone sequence contract in normal case
            expect(testResult.contracts.length).toEqual(1)
            expect(testResult.contracts[0].fields[0]).toEqual(0)
            const next1 = BigInt(1) << BigInt(seq)
            expect(testResult.contracts[0].fields[1].toString()).toEqual(next1.toString())
            expect(testResult.contracts[0].fields[2]).toEqual(0)
            expect(testResult.events.length).toEqual(0)
        }

        for (let seq = 256; seq < 512; seq++) {
            const testResult = await sequence.testPrivateMethod(client, 'checkSequence', {
                initialFields: sequenceInfo.selfState.fields,
                address: sequenceInfo.address,
                testArgs: [seq]
            })
            // won't load undone sequence contract in normal case
            expect(testResult.contracts.length).toEqual(1)
            expect(testResult.contracts[0].fields[0]).toEqual(0)
            expect(testResult.contracts[0].fields[1]).toEqual(0)
            const next2 = BigInt(1) << BigInt(seq - 256)
            expect(testResult.contracts[0].fields[2].toString()).toEqual(next2.toString())
            expect(testResult.events.length).toEqual(0)
        }
    }, 90000)

    it('should failed if sequence too large', async () => {
        const sequenceInfo = await createSequence(client, 0, allExecuted, 0n)
        const sequence = sequenceInfo.contract
        await expectAssertionFailed(async() => {
            await sequence.testPrivateMethod(client, 'checkSequence', {
                initialFields: sequenceInfo.selfState.fields,
                address: sequenceInfo.address,
                testArgs: [1024]
            })
        })
    })

    it('should move sequences to undone list', async () => {
        const next1 = (BigInt(0xff) << 248n)
        const currentSeq = 513
        const sequenceInfo = await createSequence(
            client, 0, next1, 0n, "", 100, 513 - (248 - 50)
        )
        const sequence = sequenceInfo.contract
        const testResult = await sequence.testPrivateMethod(client, 'checkSequence', {
            initialFields: sequenceInfo.selfState.fields,
            address: sequenceInfo.address,
            testArgs: [currentSeq],
            existingContracts: sequenceInfo.dependencies
        })
        let undoneList = ""
        for (let seq = 248 - 1; seq >= 248 - 50; seq--) {
            undoneList = sequenceToHex(seq) + undoneList
        }
        expect(testResult.contracts[0].fields[1]).toEqual(undoneList)
        expect(testResult.contracts[1].fields[0]).toEqual(256)
        expect(testResult.contracts[1].fields[1]).toEqual(0)
        expect(testResult.contracts[1].fields[2]).toEqual(2)
        expect(testResult.events.length).toEqual(1)

        let removed = ""
        for (let seq = 0; seq < 248 - 50; seq++) {
            removed = removed + sequenceToHex(seq)
        }
        const event = testResult.events[0]
        expect(event.fields).toEqual([removed])
    })

    it('should set sequence to done', async () => {
        const sequenceInfo = await createSequence(
            client, 256, 0n, 0n, sequenceToHex(12) + sequenceToHex(15)
        )
        const sequence = sequenceInfo.contract
        const testResult = await sequence.testPrivateMethod(client, 'checkSequence', {
            initialFields: sequenceInfo.selfState.fields,
            address: sequenceInfo.address,
            testArgs: [12],
            existingContracts: sequenceInfo.dependencies
        })
        expect(testResult.contracts[0].fields[1]).toEqual(sequenceToHex(15))
        expect(testResult.contracts[1].fields[0]).toEqual(256)
        expect(testResult.contracts[1].fields[1]).toEqual(0)
        expect(testResult.contracts[1].fields[2]).toEqual(0)
        expect(testResult.events.length).toEqual(0)

        await expectAssertionFailed(async() => {
            await sequence.testPrivateMethod(client, 'checkSequence', {
                initialFields: sequenceInfo.selfState.fields,
                address: sequenceInfo.address,
                testArgs: [14],
                existingContracts: sequenceInfo.dependencies
            })
        })
    })

    it("should increase executed sequence", async () => {
        const sequenceInfo = await createSequence(client, 512, allExecuted, allExecuted)
        const sequence = sequenceInfo.contract
        const testResult = await sequence.testPrivateMethod(client, 'checkSequence', {
            initialFields: sequenceInfo.selfState.fields,
            address: sequenceInfo.address,
            testArgs: [1025]
        })
        expect(testResult.contracts.length).toEqual(1)
        expect(testResult.contracts[0].fields[0]).toEqual(512 + 256)
        expect(testResult.contracts[0].fields[1]).toEqual(allExecuted)
        expect(testResult.contracts[0].fields[2]).toEqual(2)
        expect(testResult.events.length).toEqual(0)
    })

    test("should failed when executed repeatedly", async () => {
        const sequenceInfo = await createSequence(client, 0, allExecuted, 0n)
        const sequence = sequenceInfo.contract
        for (let seq = 0; seq < 256; seq++) {
            await expectAssertionFailed(async() => {
                return await sequence.testPrivateMethod(client, "checkSequence", {
                    initialFields: sequenceInfo.selfState.fields,
                    address: sequenceInfo.address,
                    testArgs: [seq]
                })
            })
        }

        for (let seq = 256; seq < 512; seq++) {
            await expectAssertionFailed(async() => {
                return await sequence.testPrivateMethod(client, "checkSequence", {
                    initialFields: [0, 0, allExecuted, ''],
                    address: sequenceInfo.address,
                    testArgs: [seq]
                })
            })
        }

        for (let seq = 0; seq < 512; seq++) {
            await expectAssertionFailed(async() => {
                return await sequence.testPrivateMethod(client, "checkSequence", {
                    initialFields: [512, 0, 0, ''],
                    address: sequenceInfo.address,
                    testArgs: [seq]
                })
            })
        }
    }, 180000)
})
