transaction(publicKeys: [String]) {
    prepare(payer: auth(AddKey, BorrowValue) &Account) {
        for key in publicKeys {
            // Create an account and set the account public key.
            let acct = Account(payer: payer)
            let publicKey = PublicKey(
                publicKey: key.decodeHex(),
                signatureAlgorithm: SignatureAlgorithm.ECDSA_secp256k1
            )
            acct.keys.add(
                publicKey: publicKey,
                hashAlgorithm: HashAlgorithm.SHA3_256,
                weight: 1000.0
            )
        }
    }
}