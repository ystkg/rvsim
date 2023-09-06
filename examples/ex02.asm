.text
.globl main
main:
    addi    t0, x0, 1927
    addi    t1, x0, 234
    sw      t0, (x0)
    sw      t1, 4(x0)

# addition 32 bit unsigned integer
    lw      a0, (x0)
    lw      a1, 4(x0)
    jal     addu
    addi    t0, x0, 0x10            # base address
    sw      a0, (t0)                # a0 + a1
    sw      a1, 4(t0)               # carry

# subtraction 32 bit unsigned integer
    lw      a0, (x0)                # minuend
    lw      a1, 4(x0)               # subtrahend
    jal     subu
    addi    t0, x0, 0x20            # base address
    sw      a0, (t0)                # a0 - a1
    sw      a1, 4(t0)               # borrow

# multiplication 32 bit unsigned integer
    lw      a0, (x0)                # multiplicand
    lw      a1, 4(x0)               # multiplier
    jal     mulu
    addi    t0, x0, 0x30            # base address
    sw      a0, (t0)                # a0 * a1
    sw      a1, 4(t0)               # overflow

# division 32 bit unsigned integer
    lw      a0, (x0)                # dividend
    lw      a1, 4(x0)               # divisor
    jal     divu
    addi    t0, x0, 0x40            # base address
    sw      a0, (t0)                # a0 / a1
    sw      a1, 4(t0)               # a0 % a1

    jal     x0, end

.globl addu
addu:                               # a0 = a0 + a1, a1 = carry
    add     a0, a0, a1              # sum
    sltu    a1, a0, a1              # carry
    jalr    x0, (x1)                # ret

.globl subu
subu:                               # a0 = a0 - a1, a1 = bollow
    sltu    t3, a0, a1              # borrow
    sub     a0, a0, a1              # difference
    addi    a1, t3, 0               # borrow
    jalr    x0, (x1)                # ret

.globl mulu
mulu:                               # a0 = (a0 * a1) & 0xffffffff, a1 = (a0 * a1) >> 32
    addi    t3, x0, 0               # product
    bgeu    a0, a1, mulu_init       # if multiplicand >= multiplier then no swap
    addi    t4, a0, 0               # swap to reduce loop times
    addi    a0, a1, 0
    addi    a1, t4, 0
mulu_init:
    srli    t4, a0, 16
    beq     t4, x0, mulul_loop      # if clearly no overflow then mulul_loop
    addi    t5, x0, 0               # overflow product
    addi    t6, x0, 0               # overflow floating multiplicand
mulu_loop:
    andi    t4, a1, 1               # floating multiplier
    beq     t4, x0, mulu_skip       # if least significant bit == 0 then skip
    add     t3, t3, a0              # product
    sltu    t2, t3, a0              # addition carry
    add     t5, t5, t6              # overflow product
    add     t5, t5, t2              # overflow product + addition carry
mulu_skip:
    addi    t2, a0, 0               # before
    slli    a0, a0, 1               # floating multiplicand
    sltu    t2, a0, t2              # shift carry
    slli    t6, t6, 1               # overflow floating multiplicand
    add     t6, t6, t2              # overflow floating multiplicand + shift carry
    srli    a1, a1, 1               # floating multiplier
    bne     a1, x0, mulu_loop       # if floating multiplier != 0 then loop
    addi    a0, t3, 0               # return value product
    addi    a1, t5, 0               # return value overflow product
    jalr    x0, (x1)                # ret

.globl mulul
mulul:                              # a0 = a0 * a1
    addi    t3, x0, 0               # product
    bgeu    a0, a1, mulul_loop      # if multiplicand >= multiplier then no swap
    addi    t4, a0, 0               # swap to reduce loop times
    addi    a0, a1, 0
    addi    a1, t4, 0
mulul_loop:
    andi    t4, a1, 1               # floating multiplier
    beq     t4, x0, mulul_skip      # if least significant bit == 0 then skip
    add     t3, t3, a0              # product
mulul_skip:
    slli    a0, a0, 1               # floating multiplicand
    srli    a1, a1, 1               # floating multiplier
    bne     a1, x0, mulul_loop      # if floating multiplier != 0 then loop
    addi    a0, t3, 0               # return value product
    jalr    x0, (x1)                # ret

.globl divu
divu:                               # a0 = a0 / a1, a1 = a0 % a1
    bne     a1, x0, divu_init       # division by zero
    addi    a1, a0, 0               # remainder = dividend
    addi    a0, x0, -1              # quotient = all bits set
    jalr    x0, (x1)                # ret
divu_init:
    addi    t4, x0, 0               # quotient
    addi    t3, a1, 0               # floating divisor
divu_loop1:
    bgeu    t3, a0, divu_loop2      # if floating divisor >= remainder then break
    addi    t5, t3, 0               # before
    slli    t3, t3, 1               # floating divisor
    bltu    t5, t3, divu_loop1      # if most significant bit != 1 then loop1
    addi    t3, t5, 0               # restore prev
divu_loop2:
    slli    t4, t4, 1               # quotient
    bltu    a0, t3, divu_skip       # if remainder < floating divisor then skip
    sub     a0, a0, t3              # remainder
    addi    t4, t4, 1               # quotient
divu_skip:
    srli    t3, t3, 1               # floating divisor
    bgeu    t3, a1, divu_loop2      # if floating divisor >= initial divisor  then loop2
    addi    a1, a0, 0               # return value remainder
    addi    a0, t4, 0               # return value quotient
    jalr    x0, (x1)                # ret

end:
