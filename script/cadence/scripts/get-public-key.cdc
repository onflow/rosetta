pub fun main(addr: Address, keyIndex: Int): String {
    let acct = getAccount(addr)
    let AccountKey = acct.keys.get(keyIndex: keyIndex)
    return String.encodeHex(AccountKey!.publicKey.publicKey)
}