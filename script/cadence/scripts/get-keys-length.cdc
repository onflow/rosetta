pub fun main(addr: Address): Int {
    let acct = getAccount(addr)
    var index = 0
    while true {
        let key = acct.keys.get(keyIndex: index)
        if let num = key {
            index = index + 1
        } else {
            return index
        }
    }
    return 0
}