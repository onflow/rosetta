const path = require("path");
const {
  emulator,
  executeScript,
  getAccountAddress,
  getContractAddress,
  getFlowBalance,
  getScriptCode,
  getServiceAddress,
  getTransactionCode,
  init,
  mintFlow,
  sendTransaction,
  SignatureAlgorithm,
} = require("@onflow/flow-js-testing");

const crypto = require("crypto");
const fs = require("fs");
const secp256k1 = require('secp256k1');

jest.setTimeout(10000);

const keyMessage = "some message to sign";
const keySignature = "34d188e2aa3c5e6c65b323c400b536b268b2326b49f1d69467753ffc0a5ad53851e8e9552deddea020b174dfc3d4c91d1c435867010f73c38d1c760817069e47";
const privateKey = "be92543651fed1d4c76f33e568bd81cfb6d3669c2be9a11499e1111e0e2fd46b";
const privateKey2 = "4c7d205980a81a7d27f66ba9081da9772630f5281c6f187e9c833e4b30c3da3d";
const privateKey3 = "fa2c51754b79850acf88be53900d529e0ed97499d5656d75a63a98b94503a7d1";
const publicKey = "ae89c798171980471c0648957ceaf305d3251d7e5c12d3eea3393512680876d2db8cc59aec316dc9bf3d14d5a0ea5b0f6fa072ba6c5c6cfd607bb710cfe0bfda";
const publicKey3 = "5a7a5a204e7207c02121f5444a47ec29cc34495926b7136ea1e2169dd5445dcc2a6e29cb109cb8da0b65762543989e5375d5d4ab4e7c77ab5d3c194e046fe1e8";
const userTag = "FLOW-V0.0-user\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00";

let FlowColdStorageProxy;
let prevKeyIndex;
let prevPublicKey;
let prevTransferArgs;

function computeSigDataHex(nonce, amount, receiver, privateKey) {
  const nonceBuffer = Buffer.alloc(8);
  nonceBuffer.writeBigUInt64BE(BigInt(nonce));
  const amountBuffer = Buffer.alloc(8);
  amountBuffer.writeBigUInt64BE(BigInt(amount) * 100000000n);
  const receiverBuffer = Buffer.from(receiver.slice(2), "hex");

  const hashResult = crypto.createHash("sha3-256")
    .update(userTag)
    .update(receiverBuffer)
    .update(amountBuffer)
    .update(nonceBuffer)
    .digest();

  const privateKeyBuffer = Buffer.from(privateKey, "hex");
  const sig = secp256k1.ecdsaSign(hashResult, privateKeyBuffer);
  const sigData = [];
  for (const elem of sig.signature) {
    sigData.push(elem.toString(16).padStart(2, '0'));
  };

  return sigData.join('');
}

function invalidSigDataHex(nonce, amount, receiver) {
  return computeSigDataHex(nonce, amount, receiver, privateKey2);
}

function sigDataHex(nonce, amount, receiver) {
  return computeSigDataHex(nonce, amount, receiver, privateKey);
}

describe("Test FlowColdStorageProxy contract", () => {
  beforeAll(async () => {
    const basePath = path.resolve(__dirname, "./cadence");
    await init(basePath);
    return emulator.start();
  });

  afterAll(async () => {
    return emulator.stop();
  });

  test("Create account, deposit and check balance - FlowAccount", async () => {
    const FlowAccount = await getAccountAddress("FlowAccount");
    await mintFlow(FlowAccount, "5.0");
    const balance = await getFlowBalance(FlowAccount);

    expect(balance[0]).toBe("5.00100000");
  });

  test("Create account, deposit and check balance - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");
    FlowColdStorageProxy = ProxyDeployAccount;
    await mintFlow(ProxyDeployAccount, "6.0");
    const balance = await getFlowBalance(ProxyDeployAccount);

    expect(balance[0]).toBe("6.00100000");
  });

  test("Create account, deposit and check balance - ProxyPayer", async () => {
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    await mintFlow(ProxyPayer, "3.0");
    const balance = await getFlowBalance(ProxyPayer);

    expect(balance[0]).toBe("3.00100000");
  });

  test("Deploy proxy contract - to ProxyDeployAccount", async () => {
    const name = "FlowColdStorageProxy";

    const argsLength = [FlowColdStorageProxy];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const index = parseInt(len, 10) - 1;

    const argsIndex = [FlowColdStorageProxy, index.toString()];
    const [keyIndexValue, errIndex] = await executeScript({
      name: "get-key-index",
      args: argsIndex
    });

    expect(errIndex).toBe(null);

    const keyIndex = parseInt(keyIndexValue, 10);
    const data = fs.readFileSync("cadence/contracts/FlowColdStorageProxy.cdc");

    const args = [false, name, data.toString(), keyIndex.toString(), publicKey3, keyMessage, keySignature, "keyid123"];
    const signers = [
      {
        addr: FlowColdStorageProxy,
        keyId: keyIndex,
      },
    ];

    const [tx, err] = await sendTransaction({ name: "proxy-contract-update", signers, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);
  });

  test("Register proxy contract with FlowManager", async () => {
    const FlowManager = await getServiceAddress();

    const addressMap = { FlowManager };
    const txTemplate = await getTransactionCode({
      name: "register-contract",
      addressMap,
    });

    const args = ["FlowColdStorageProxy", FlowColdStorageProxy, FlowManager];
    const [tx, err] = await sendTransaction({ code: txTemplate, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);

    const address = await getContractAddress("FlowColdStorageProxy");

    expect(address).toBe(FlowColdStorageProxy);
  });

  test("Get basic balance - FlowAccount", async () => {
    const FlowAccount = await getAccountAddress("FlowAccount");
    const args = [FlowAccount];
    const name = "get-balances-basic";
    const [accountBalance, err] = await executeScript({
      name,
      args
    });

    expect(err).toBe(null);
    expect(accountBalance.default_balance).toBe("5.00100000");
    expect(accountBalance.is_proxy).toBe(false);
    expect(accountBalance.proxy_balance).toBe("0.00000000");
  });

  test("Create proxy account - ProxyAccountA", async () => {
    const FlowManager = await getServiceAddress();

    const addressMap = { FlowColdStorageProxy, FlowManager };
    const txTemplate = await getTransactionCode({
      name: "create-proxy-account",
      addressMap,
    });

    const args = ["ProxyAccountA", publicKey, FlowManager];
    const FlowAccount = await getAccountAddress("FlowAccount");
    const signers = [FlowAccount];
    const [tx, err] = await sendTransaction({ code: txTemplate, signers, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);

    let proxyAccountA;
    for (const event of tx.events) {
      if (event.type === "flow.AccountCreated") {
        proxyAccountA = event.data.address;
      }
    };

    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    expect(proxyAccountA).toBe(ProxyAccountA);
  });

  test("Deposit and get proxy balance - ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    await mintFlow(ProxyAccountA, "7.0");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-balances",
      addressMap,
    });

    const args = [ProxyAccountA];
    const [accountBalance, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(accountBalance.default_balance).toBe("0.00100000");
    expect(accountBalance.is_proxy).toBe(true);
    expect(accountBalance.proxy_balance).toBe("7.00000000");
  });

  test("Get proxy nonce - ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const args = [ProxyAccountA];
    const [nonceValue, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    expect(nonce).toBe(0);
  });

  test("Create proxy account - ProxyAccountB", async () => {
    const FlowManager = await getServiceAddress();

    const addressMap = { FlowColdStorageProxy, FlowManager };
    const txTemplate = await getTransactionCode({
      name: "create-proxy-account",
      addressMap,
    });

    const args = ["ProxyAccountB", publicKey, FlowManager];
    const FlowAccount = await getAccountAddress("FlowAccount");
    const signers = [FlowAccount];
    const [tx, err] = await sendTransaction({ code: txTemplate, signers, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);

    let proxyAccountB;
    for (const event of tx.events) {
      if (event.type === "flow.AccountCreated") {
        proxyAccountB = event.data.address;
      }
    };

    const ProxyAccountB = await getAccountAddress("ProxyAccountB");

    expect(proxyAccountB).toBe(ProxyAccountB);
  });

  test("Deposit and get proxy balance - ProxyAccountB", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    await mintFlow(ProxyAccountB, "8.0");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-balances",
      addressMap,
    });

    const args = [ProxyAccountB];
    const [accountBalance, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(accountBalance.default_balance).toBe("0.00100000");
    expect(accountBalance.is_proxy).toBe(true);
    expect(accountBalance.proxy_balance).toBe("8.00000000");
  });

  test("Get proxy nonce - ProxyAccountB", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const args = [ProxyAccountB];
    const [nonceValue, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    expect(nonce).toBe(0);
  });

  test("Deposit FLOW to ProxyAccountA from FlowAccount", async () => {
    const name = "basic-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    const args = [ProxyAccountA, 1.0];
    const FlowAccount = await getAccountAddress("FlowAccount");
    const signers = [FlowAccount];
    const [tx, err] = await sendTransaction({ name, signers, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);
  });

  test("Get balances after normal to proxy transfer - from FlowAccount to ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-balances",
      addressMap,
    });

    const FlowAccount = await getAccountAddress("FlowAccount");
    const args = [FlowAccount];
    const [accountBalance, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const balance = parseFloat(accountBalance.default_balance);
    const argsProxy = [ProxyAccountA];
    const [accountBalanceProxy, errProxy] = await executeScript({
      code: scriptTemplate,
      args: argsProxy
    });

    expect(errProxy).toBe(null);
    expect(accountBalance.is_proxy).toBe(false);
    expect(accountBalance.proxy_balance).toBe("0.00000000");
    expect(accountBalanceProxy.default_balance).toBe("0.00100000");
    expect(accountBalanceProxy.is_proxy).toBe(true);
    expect(accountBalanceProxy.proxy_balance).toBe("8.00000000");
    expect(balance).toBeLessThan(4.001);
  });

  test("Deposit FLOW to FlowAccount from ProxyAccountB", async () => {
    const nameTransfer = "proxy-transfer";
    const FlowAccount = await getAccountAddress("FlowAccount");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");

    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const [nonceValue, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountB]
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 2.0;

    const sigHex = sigDataHex(nonce, amount, FlowAccount);
    const args = [ProxyAccountB, FlowAccount, amount, nonce.toString(), sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).toBe(null);
    expect(tx).not.toBe(null);
  });

  test("Get balances after proxy to normal transfer - from ProxyAccountB to FlowAccount", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-balances",
      addressMap,
    });

    const FlowAccount = await getAccountAddress("FlowAccount");
    const args = [FlowAccount];
    const [accountBalance, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const balance = parseFloat(accountBalance.default_balance);
    const argsProxy = [ProxyAccountB];
    const [accountBalanceProxy, errProxy] = await executeScript({
      code: scriptTemplate,
      args: argsProxy
    });

    expect(errProxy).toBe(null);
    expect(accountBalance.is_proxy).toBe(false);
    expect(accountBalance.proxy_balance).toBe("0.00000000");
    expect(accountBalanceProxy.default_balance).toBe("0.00100000");
    expect(accountBalanceProxy.is_proxy).toBe(true);
    expect(accountBalanceProxy.proxy_balance).toBe("6.00000000");
    expect(balance).toBeLessThan(6.001);
  });

  test("Get proxy nonce after a transfer - ProxyAccountB", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const args = [ProxyAccountB];
    const [nonceValue, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    expect(nonce).toBe(1);
  });

  test("Deposit FLOW to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const [nonceValue, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 3.0;

    const sigHex = sigDataHex(nonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, nonce.toString(), sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).toBe(null);
    expect(tx).not.toBe(null);
    prevTransferArgs = args;
  });

  test("Get balances after proxy to proxy transfer - from ProxyAccountA to ProxyAccountB", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-balances",
      addressMap,
    });

    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const args = [ProxyAccountA];
    const [proxyBalanceA, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const argsProxy = [ProxyAccountB];
    const [proxyBalanceB, errProxy] = await executeScript({
      code: scriptTemplate,
      args: argsProxy
    });

    expect(errProxy).toBe(null);
    expect(proxyBalanceA.default_balance).toBe("0.00100000");
    expect(proxyBalanceA.is_proxy).toBe(true);
    expect(proxyBalanceA.proxy_balance).toBe("5.00000000");
    expect(proxyBalanceB.default_balance).toBe("0.00100000");
    expect(proxyBalanceB.is_proxy).toBe(true);
    expect(proxyBalanceB.proxy_balance).toBe("9.00000000");
  });

  test("Get proxy nonce after a transfer - ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const args = [ProxyAccountA];
    const [nonceValue, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    expect(nonce).toBe(1);
  });

  test("Check replay defense for deposit of FLOW to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyPayer = await getAccountAddress("ProxyPayer");

    const addressMap = { FlowColdStorageProxy };

    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const args = prevTransferArgs;
    const signers = [ProxyPayer];
    const [tx, err] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(err).not.toBe(null);
    expect(tx).toBe(null);
  });

  test("Deposit FLOW with invalid nonce - to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const [nonceValue, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    const invalidNonce = nonce + 1;
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 1.0;

    const sigHex = sigDataHex(invalidNonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, invalidNonce.toString(), sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).not.toBe(null);
    expect(tx).toBe(null);
  });

  test("Deposit FLOW with signature of wrong nonce - to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");

    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const [nonceValue, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    const invalidNonce = nonce - 1;
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 1.0;

    const sigHex = sigDataHex(invalidNonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, nonce.toString(), sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).not.toBe(null);
    expect(tx).toBe(null);
  });

  test("Deposit FLOW with invalid signature - to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");

    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });

    const [nonceValue, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });

    expect(err).toBe(null);

    const nonce = parseInt(nonceValue, 10);

    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 1.0;

    const sigHex = invalidSigDataHex(nonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, nonce.toString(), sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).not.toBe(null);
    expect(tx).toBe(null);
  });

  test("Get key index before contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const argsLength = [ProxyDeployAccount];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const keyIndex = parseInt(len, 10) - 1;

    const argsIndex = [ProxyDeployAccount, keyIndex.toString()];
    const [key, errIndex] = await executeScript({
      name: "get-key-index",
      args: argsIndex
    });

    expect(errIndex).toBe(null);

    prevKeyIndex = parseInt(key, 10);

    expect(prevKeyIndex).toBe(keyIndex);
  });

  test("Get public key before contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const argsLength = [ProxyDeployAccount];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const keyIndex = parseInt(len, 10) - 1;

    const argsKey = [ProxyDeployAccount, keyIndex.toString()];
    const [key, errKey] = await executeScript({
      name: "get-public-key",
      args: argsKey
    });

    expect(errKey).toBe(null);
    expect(key).not.toBe(null);
    prevPublicKey = key;
  });

  test("Get revoke status of key before contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const argsLength = [ProxyDeployAccount];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const keyIndex = parseInt(len, 10) - 1;

    const argsStatus = [ProxyDeployAccount, keyIndex.toString()];
    const [status, errStatus] = await executeScript({
      name: "get-revoke-status",
      args: argsStatus
    });

    expect(errStatus).toBe(null);
    expect(status).toBe(false);
  });

  test("Update proxy contract - to ProxyDeployAccount", async () => {
    const name = "FlowColdStorageProxy";
    const address = await getContractAddress(name);
    const dataFromFile = fs.readFileSync("cadence/contracts/FlowColdStorageProxy.cdc");
    const data = dataFromFile.toString().replace("AUTOMATED TESTS.", `

      pub fun newMethod(): String {
        return "proxy contract updated"
      }

    `);

    const args = [true, name, data, prevKeyIndex.toString(), publicKey3, keyMessage, keySignature, "keyid123"];
    const signers = [{
      addr: address,
      privateKey: privateKey3,
      signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1,
      keyId: prevKeyIndex,
    }];
    const [tx, err] = await sendTransaction({ name: "proxy-contract-update", signers, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);
    prevKeyIndex++;
  });

  test("Update proxy contract with invalid message - to ProxyDeployAccount", async () => {
    const name = "FlowColdStorageProxy";
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const data = fs.readFileSync("cadence/contracts/FlowColdStorageProxy.cdc");

    const args = [true, name, data.toString(), prevKeyIndex.toString(), publicKey3, keyMessage + "x", keySignature, "keyid123"];
    const signers = [{
      addr: ProxyDeployAccount,
      privateKey: privateKey3,
      signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1,
      keyId: prevKeyIndex,
    }];

    const [tx, err] = await sendTransaction({ name: "proxy-contract-update", signers, args });

    expect(tx).toBe(null);
    expect(err).not.toBe(null);
    expect(err).toContain("Key cannot be verified");
  });

  test("Update proxy contract with invalid past key index - to ProxyDeployAccount", async () => {
    const name = "FlowColdStorageProxy";
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const data = fs.readFileSync("cadence/contracts/FlowColdStorageProxy.cdc");

    const args = [true, name, data.toString(), (prevKeyIndex - 1).toString(), publicKey3, keyMessage, keySignature, "keyid123"];
    const signers = [{
      addr: ProxyDeployAccount,
      privateKey: privateKey3,
      signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1,
      keyId: prevKeyIndex,
    }];

    const [tx, err] = await sendTransaction({ name: "proxy-contract-update", signers, args });

    expect(tx).toBe(null);
    expect(err).not.toBe(null);
    expect(err).toContain("Invalid prevKeyIndex, found key at next key index");
  });

  test("Update proxy contract with invalid future key index - to ProxyDeployAccount", async () => {
    const name = "FlowColdStorageProxy";
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const data = fs.readFileSync("cadence/contracts/FlowColdStorageProxy.cdc");

    const args = [true, name, data.toString(), (prevKeyIndex + 1).toString(), publicKey3, keyMessage, keySignature, "keyid123"];
    const signers = [{
      addr: ProxyDeployAccount,
      privateKey: privateKey3,
      signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1,
      keyId: prevKeyIndex,
    }];

    const [tx, err] = await sendTransaction({ name: "proxy-contract-update", signers, args });

    expect(tx).toBe(null);
    expect(err).not.toBe(null);
    expect(err).toContain("Invalid prevKeyIndex, didn't find matching key");
  });

  test("Get revoke status of previous key after contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const argsLength = [ProxyDeployAccount];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const keyIndex = parseInt(len, 10) - 1;

    const argsStatus = [ProxyDeployAccount, (keyIndex - 1).toString()];
    const [status, errStatus] = await executeScript({
      name: "get-revoke-status",
      args: argsStatus
    });

    expect(errStatus).toBe(null);
    expect(status).toBe(true);
  });

  test("Get key index after contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const argsLength = [ProxyDeployAccount];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const keyIndexValue = parseInt(len, 10) - 1;

    const argsIndex = [ProxyDeployAccount, keyIndexValue.toString()];
    const [keyIndex, errIndex] = await executeScript({
      name: "get-key-index",
      args: argsIndex
    });

    const key = parseInt(keyIndex, 10);

    expect(errIndex).toBe(null);
    expect(key).toBe(prevKeyIndex);
  });

  test("Get public key after contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const argsLength = [ProxyDeployAccount];
    const [len, errLength] = await executeScript({
      name: "get-keys-length",
      args: argsLength
    });

    expect(errLength).toBe(null);

    const keyIndex = parseInt(len, 10) - 1;

    const argsKey = [ProxyDeployAccount, keyIndex.toString()];
    const [key, errKey] = await executeScript({
      name: "get-public-key",
      args: argsKey
    });

    expect(errKey).toBe(null);
    expect(key).toBe(publicKey3);
    expect(key).toBe(prevPublicKey);
  });

  test("Call the new method on updated contract - ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-newmethod",
      addressMap,
    });

    const args = [ProxyAccountA];
    const [msg, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(msg).toBe("proxy contract updated");
  });

  test("Get the length of active keys after contract update - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const args = [ProxyDeployAccount];
    const [len, err] = await executeScript({
      name: "get-active-keys-length",
      args
    });

    expect(err).toBe(null);

    const length = parseInt(len, 10);

    expect(length).toBe(1);
  });

  test("Update proxy contract again - to ProxyDeployAccount", async () => {
    const name = "FlowColdStorageProxy";
    const address = await getContractAddress(name);

    const dataFromFile = fs.readFileSync("cadence/contracts/FlowColdStorageProxy.cdc");
    const data = dataFromFile.toString().replace("AUTOMATED TESTS.", `

      pub fun newMethod(): String {
        return "proxy contract updated again"
      }

    `);

    const args = [true, name, data, prevKeyIndex.toString(), publicKey3, keyMessage, keySignature, "keyid123"];
    const signers = [{
      addr: address,
      privateKey: privateKey3,
      signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1,
      keyId: prevKeyIndex,
    }];

    const [tx, err] = await sendTransaction({ name: "proxy-contract-update", signers, args });

    expect(err).toBe(null);
    expect(tx).not.toBe(null);
    prevKeyIndex++;
  });

  test("Call the new method on updated contract again - ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");

    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-newmethod",
      addressMap,
    });

    const args = [ProxyAccountA];
    const [msg, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(msg).toBe("proxy contract updated again");
  });

  test("Get the length of active keys after contract update again - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const args = [ProxyDeployAccount];
    const [len, err] = await executeScript({
      name: "get-active-keys-length",
      args
    });

    expect(err).toBe(null);

    const length = parseInt(len, 10);

    expect(length).toBe(1);
  });

  test("Get the total number of keys - ProxyDeployAccount", async () => {
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");

    const args = [ProxyDeployAccount];
    const [len, err] = await executeScript({
      name: "get-keys-length",
      args
    });

    expect(err).toBe(null);

    const index = parseInt(len, 10) - 1;

    expect(index).toBe(prevKeyIndex);
  });
});