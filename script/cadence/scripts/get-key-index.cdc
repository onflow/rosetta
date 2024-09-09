access(all) fun main(addr: Address, keyIndex: Int): Int {
    let acct = getAccount(addr)
    let AccountKey = acct.keys.get(keyIndex: keyIndex)
    return AccountKey!.keyIndex
}