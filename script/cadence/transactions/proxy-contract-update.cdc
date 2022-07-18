transaction(contractName: String, contractCode: String, prevKeyIndex: Int, newKey: String, newKeyHash: String) {
    prepare(payer: AuthAccount) {
        let hash = HashAlgorithm.SHA3_256.hash(newKey.decodeHex())
        if String.encodeHex(hash) != newKeyHash {
            let msg = "Mismatching hash for new signing key: expected "
                .concat(String.encodeHex(hash))
                .concat(", got ")
                .concat(newKeyHash)
            panic(msg)
        }
        payer.contracts.update__experimental(
            name: contractName,
            code: contractCode.utf8
        )
        let publicKey = PublicKey(
            publicKey: newKey.decodeHex(),
            signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
        )
        payer.keys.add(
            publicKey: publicKey,
            hashAlgorithm: HashAlgorithm.SHA3_256,
            weight: 1000.0
        )
        payer.keys.revoke(keyIndex: prevKeyIndex)
    }
}
