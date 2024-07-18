access(all) fun main(addr: Address, keyIndex: Int): Bool {
    let acct = getAccount(addr)
    let AccountKey = acct.keys.get(keyIndex: keyIndex)
        ?? panic("No key found for ".concat(addr.toString()).concat(" at index ").concat(keyIndex.toString()))
    return String.encodeHex(AccountKey.publicKey.publicKey)
}