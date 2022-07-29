transaction(update: Bool, contractName: String, contractCode: String, prevKeyIndex: Int, newKey: String, keyMessage: String, keySignature: String, keyMetadata: String) {
    prepare(payer: AuthAccount) {
        let key = PublicKey(
            publicKey: newKey.decodeHex(),
            signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
        )
        let verified = key.verify(
            signature: keySignature.decodeHex(),
            signedData: keyMessage.utf8,
            domainSeparationTag: "",
            hashAlgorithm: HashAlgorithm.SHA2_256
        )
        if !verified {
            panic("Key cannot be verified")
        }
        let prevKey = payer.keys.get(keyIndex: prevKeyIndex)
        if prevKey == nil {
            panic("Invalid prevKeyIndex, didn't find matching key")
        }
        let nextKey = payer.keys.get(keyIndex: prevKeyIndex + 1)
        if nextKey != nil {
            panic("Invalid prevKeyIndex, found key at next key index")
        }
        if update {
            payer.contracts.update__experimental(
                name: contractName,
                code: contractCode.utf8
            )
        } else {
            payer.contracts.add(
                name: contractName,
                code: contractCode.utf8
            )
        }
        payer.keys.add(
            publicKey: key,
            hashAlgorithm: HashAlgorithm.SHA3_256,
            weight: 1000.0
        )
        payer.keys.revoke(keyIndex: prevKeyIndex)
    }
}
