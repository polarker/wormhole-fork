import { CliqueClient, Signer } from 'alephium-js'
import { Wormhole } from '../lib/wormhole'

if (process.argv.length < 3) {
    throw Error('invalid args, expect rpc port arg')
}

const port = process.argv[2]
const governanceChainId = 1
const governanceChainAddress = '0000000000000000000000000000000000000000000000000000000000000004'
const dustAmount = BigInt("1000000000000")

const client = new CliqueClient({baseUrl: `http://127.0.0.1:${port}`})
const initGuardianSet = ["beFA429d57cD18b7F8A4d91A2da9AB4AF05d0FBe"]
const initGuardianIndex = 0
const wormhole = new Wormhole(
    client,
    Signer.testSigner(client),
    governanceChainId,
    governanceChainAddress,
    governanceChainId,
    governanceChainAddress,
    initGuardianSet,
    initGuardianIndex
)

async function registerChains(tokenBridgeAddress: string) {
    const payer = "12LgGdbjE6EtnTKw5gdBwV2RRXuXPtzYM7SDZ45YJTRht"
    const alphAmount = BigInt("1000000000000000000")
    const vaas = [
        // ETH, sequence = 0
        '01000000000100e2e1975d14734206e7a23d90db48a6b5b6696df72675443293c6057dcb936bf224b5df67d32967adeb220d4fe3cb28be515be5608c74aab6adb31099a478db5c1c000000010000000100010000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000546f6b656e42726964676501000000020000000000000000000000000290fb167208af455bb137780163b7b7a9a10c16',
        // Terra, sequence = 1
        '01000000000100e7d8469492e85b4f0df03a8bc1cdbf395f843ea181fb47188fcbf67b6df621fd005fad7ae7215752f0bfc6ff0f894dc4793cb46428e7582a369cfaeb05f334f31b000000010000000100010000000000000000000000000000000000000000000000000000000000000004000000000000000100000000000000000000000000000000000000000000546f6b656e4272696467650100000003000000000000000000000000784999135aaa8a3ca5914468852fdddbddd8789d',
        // Solana, sequence = 2
        '010000000001001501232cc660aab7e2a84099fa7823048c2cab4834b1c3579656bd02b8686134150d3d69ab5bfda5aec1aa53bb3fcd89969462faa3cfd08551b36ed45b1202851c000000010000000100010000000000000000000000000000000000000000000000000000000000000004000000000000000200000000000000000000000000000000000000000000546f6b656e4272696467650100000001c69a1b1a65dd336bf1df6a77afb501fc25db7fc0938cb08595a9ef473265cb4f',
        // BSC, sequence = 3
        '01000000000100f2766a939e1cde40d3a39218c4eaac273469f3e05edb7e55c0897eb7d565432550cbbec75013abfa30a3160d3915ffa7b6232c7062ea5fd8db62ba6bff6928691c000000010000000100010000000000000000000000000000000000000000000000000000000000000004000000000000000300000000000000000000000000000000000000000000546f6b656e42726964676501000000040000000000000000000000000290fb167208af455bb137780163b7b7a9a10c16'
    ]
    const params = {
        alphAmount: alphAmount,
        gas: 500000
    }

    var txId = await wormhole.registerChainToAlph(tokenBridgeAddress, vaas[0], payer, dustAmount, params)
    // tokenBridgeForChainId: y9dvJcZAQUjgx3hL5ZGwvT488cpdpy7N6TDSK7Vk8TWs
    console.log("register eth txId: " + txId)
    txId = await wormhole.registerChainToAlph(tokenBridgeAddress, vaas[1], payer, dustAmount, params)
    // tokenBridgeForChainId: wTFbhHDHE8QhWiWXRSLrX8T8ANcn1pWwHKxf9R3shtQm
    console.log("register terra txId: " + txId)
    txId = await wormhole.registerChainToAlph(tokenBridgeAddress, vaas[2], payer, dustAmount, params)
    // tokenBridgeForChainId: 25ED4e59Nb1oqpcE7bRyDnyC13fLuV9xkVTe3KZuQKHYf
    console.log("register solana txId: " + txId)
    txId = await wormhole.registerChainToAlph(tokenBridgeAddress, vaas[3], payer, dustAmount, params)
    // tokenBridgeForChainId: 29jMRScyxsiQ1W2aGRB2JYUpXb19nEfMv916AAfmneiv1
    console.log("register bsc txId: " + txId)
}

async function deployAndRegisterChains() {
    const contracts = await wormhole.deployContracts()
    console.log('wormhole contracts: \n')
    console.log(contracts)
    await registerChains(contracts.tokenBridge.address)
}

deployAndRegisterChains()

// governance: 2AiLoHmb7C7a54z4wvQ6o35aHr7y8oPnN9q5t3KjpvBvY
// tokenBridge: 2A4W7j8kN1maGDg3xxK8eJ8Ra3XNsfg6UhPfgJ3wEs2i2
// tokenWrapperFactory: 2AqKqXdCeUtQKEX35xRSwPd8rGZWBRcUCeLwXaFArctk7
