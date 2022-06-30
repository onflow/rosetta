const path = require("path");
const {
  deployContractByName,
  emulator,
  executeScript,
  getAccountAddress,
  getContractAddress,
  getFlowBalance,
  getServiceAddress,
  getTransactionCode,
  init,
  mintFlow,
  sendTransaction,
  getScriptCode,
} = require("flow-js-testing");

const crypto = require("crypto");
const secp256k1 = require('secp256k1');

jest.setTimeout(10000);

const publicKey = "ae89c798171980471c0648957ceaf305d3251d7e5c12d3eea3393512680876d2db8cc59aec316dc9bf3d14d5a0ea5b0f6fa072ba6c5c6cfd607bb710cfe0bfda";
const publicKeySEC = "02ae89c798171980471c0648957ceaf305d3251d7e5c12d3eea3393512680876d2";
const privateKey = "be92543651fed1d4c76f33e568bd81cfb6d3669c2be9a11499e1111e0e2fd46b";
const privateKey2 = "4c7d205980a81a7d27f66ba9081da9772630f5281c6f187e9c833e4b30c3da3d";
const userTag = "FLOW-V0.0-user\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00";

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
    const port = 8080;
    await init(basePath, { port });
    return emulator.start(port);
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
    const ProxyDeployAccount = await getAccountAddress("ProxyDeployAccount");
    await deployContractByName(name, ProxyDeployAccount);
    const address = await getContractAddress(name);

    expect(address).toBe(ProxyDeployAccount);
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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy, FlowManager };
    const txTemplate = await getTransactionCode({
      name: "create-proxy-account",
      addressMap,
    });
    const args = ["ProxyAccountA", publicKey, FlowManager];
    const FlowAccount = await getAccountAddress("FlowAccount");
    const signers = [FlowAccount];
    const [tx, error] = await sendTransaction({ code: txTemplate, signers, args });

    expect(error).toBe(null);

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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const args = [ProxyAccountA];
    const [nonce, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(nonce).toBe(0);
  });

  test("Create proxy account - ProxyAccountB", async () => {
    const FlowManager = await getServiceAddress();
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy, FlowManager };
    const txTemplate = await getTransactionCode({
      name: "create-proxy-account",
      addressMap,
    });
    const args = ["ProxyAccountB", publicKey, FlowManager];
    const FlowAccount = await getAccountAddress("FlowAccount");
    const signers = [FlowAccount];
    const [tx, error] = await sendTransaction({ code: txTemplate, signers, args });

    expect(error).toBe(null);

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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const args = [ProxyAccountB];
    const [nonce, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(nonce).toBe(0);
  });

  test("Deposit FLOW to ProxyAccountA from FlowAccount", async () => {
    const name = "basic-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const args = [ProxyAccountA, 1.0];
    const FlowAccount = await getAccountAddress("FlowAccount");
    const signers = [FlowAccount];
    const [tx, error] = await sendTransaction({ name, signers, args });

    expect(error).toBe(null);
  });

  test("Get balances after normal to proxy transfer - from FlowAccount to ProxyAccountA", async () => {
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
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
    const balance = parseFloat(accountBalance.default_balance);
    const argsProxy = [ProxyAccountA];
    const [accountBalanceProxy, errProxy] = await executeScript({
      code: scriptTemplate,
      args: argsProxy
    });

    expect(err).toBe(null);
    expect(errProxy).toBe(null);
    expect(balance).toBeLessThan(4.001);
    expect(accountBalance.is_proxy).toBe(false);
    expect(accountBalance.proxy_balance).toBe("0.00000000");
    expect(accountBalanceProxy.default_balance).toBe("0.00100000");
    expect(accountBalanceProxy.is_proxy).toBe(true);
    expect(accountBalanceProxy.proxy_balance).toBe("8.00000000");
  });

  test("Deposit FLOW to FlowAccount from ProxyAccountB", async () => {
    const nameTransfer = "proxy-transfer";
    const FlowAccount = await getAccountAddress("FlowAccount");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const [nonce, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountB]
    });
    expect(err).toBe(null);
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 2.0;

    let sigHex = sigDataHex(nonce, amount, FlowAccount);
    const args = [ProxyAccountB, FlowAccount, amount, nonce, sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });
    expect(error).toBe(null);
  });

  test("Get balances after proxy to normal transfer - from ProxyAccountB to FlowAccount", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
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
    const balance = parseFloat(accountBalance.default_balance);
    const argsProxy = [ProxyAccountB];
    const [accountBalanceProxy, errProxy] = await executeScript({
      code: scriptTemplate,
      args: argsProxy
    });

    expect(err).toBe(null);
    expect(errProxy).toBe(null);
    expect(balance).toBeLessThan(6.001);
    expect(accountBalance.is_proxy).toBe(false);
    expect(accountBalance.proxy_balance).toBe("0.00000000");
    expect(accountBalanceProxy.default_balance).toBe("0.00100000");
    expect(accountBalanceProxy.is_proxy).toBe(true);
    expect(accountBalanceProxy.proxy_balance).toBe("6.00000000");
  });

  test("Get proxy nonce after a transfer - ProxyAccountB", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const args = [ProxyAccountB];
    const [nonce, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(nonce).toBe(1);
  });

  test("Deposit FLOW to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const [nonce, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });
    expect(err).toBe(null);
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 3.0;

    let sigHex = sigDataHex(nonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, nonce, sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).toBe(null);
    prevTransferArgs = args;
  });

  test("Get balances after proxy to proxy transfer - from ProxyAccountA to ProxyAccountB", async () => {
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
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
    const argsProxy = [ProxyAccountB];
    const [proxyBalanceB, errProxy] = await executeScript({
      code: scriptTemplate,
      args: argsProxy
    });

    expect(err).toBe(null);
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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };
    const scriptTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const args = [ProxyAccountA];
    const [nonce, err] = await executeScript({
      code: scriptTemplate,
      args
    });

    expect(err).toBe(null);
    expect(nonce).toBe(1);
  });

  test("Check replay defense for deposit of FLOW to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };

    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const args = prevTransferArgs;
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).not.toBe(null);
  });

  test("Deposit FLOW with invalid nonce - to ProxyAccountB from ProxyAccountA", async () => {
    const nameTransfer = "proxy-transfer";
    const ProxyAccountA = await getAccountAddress("ProxyAccountA");
    const ProxyAccountB = await getAccountAddress("ProxyAccountB");
    const ProxyPayer = await getAccountAddress("ProxyPayer");
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const [nonce, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });
    expect(err).toBe(null);
    let invalidNonce = nonce + 1;
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 1.0;

    let sigHex = sigDataHex(invalidNonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, invalidNonce, sigHex];
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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const [nonce, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });
    expect(err).toBe(null);
    let invalidNonce = nonce - 1;
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 1.0;

    let sigHex = sigDataHex(invalidNonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, nonce, sigHex];
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
    const FlowColdStorageProxy = await getContractAddress("FlowColdStorageProxy");
    const addressMap = { FlowColdStorageProxy };

    const scriptNonceTemplate = await getScriptCode({
      name: "get-proxy-nonce",
      addressMap,
    });
    const [nonce, err] = await executeScript({
      code: scriptNonceTemplate,
      args: [ProxyAccountA]
    });
    expect(err).toBe(null);
    const scriptTransferTemplate = await getTransactionCode({
      name: nameTransfer,
      addressMap,
    });

    const amount = 1.0;

    let sigHex = invalidSigDataHex(nonce, amount, ProxyAccountB);
    const args = [ProxyAccountA, ProxyAccountB, amount, nonce, sigHex];
    const signers = [ProxyPayer];
    const [tx, error] = await sendTransaction({ code: scriptTransferTemplate, signers, args });

    expect(error).not.toBe(null);
    expect(tx).toBe(null);
  });
});